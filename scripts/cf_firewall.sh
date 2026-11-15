#!/usr/bin/env bash
# 只允许 Cloudflare 网段访问本机发布的 80/443。
#
# 为什么不用 ufw：Docker 发布端口走 FORWARD / DOCKER 链，ufw 的 INPUT 规则拦不住。
# 这里把规则装进 DOCKER-USER（Docker 留给用户的链），在转发到容器之前生效。
#
# 用法（在服务器上，仓库根目录）：
#   ./scripts/cf_firewall.sh                # 只打印将执行的规则，不生效
#   sudo ./scripts/cf_firewall.sh --apply   # 真正装规则
#   sudo ./scripts/cf_firewall.sh --check   # 看当前装了什么
#   sudo ./scripts/cf_firewall.sh --apply --ports 80,443
#
# 注意：
# - 装完以后，绕过 Cloudflare 直连源站 IP 的 80/443 会被丢包，包括监控探针。
#   想留白名单就改 PORTS 或先在本机用 `curl --resolve` 验证。
# - SSH（22）不受影响：它走 INPUT，不经过 DOCKER-USER。
# - 用 Cloudflare Tunnel 时不要跑这个脚本，没有入站端口要保护。
# - 重启后规则默认不保留；用 `--persist` 交给 netfilter-persistent，或者加一条
#   `@reboot` 定时任务重跑。
set -euo pipefail

PORTS="80,443"
MODE="dry-run"

while [ $# -gt 0 ]; do
	case "$1" in
		--apply) MODE="apply" ;;
		--check) MODE="check" ;;
		--persist) MODE="apply"; PERSIST=1 ;;
		--ports)
			[ $# -ge 2 ] || { echo "错误：--ports 后面要跟端口列表" >&2; exit 2; }
			PORTS="$2"; shift ;;
		-h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "错误：未知参数 $1" >&2; exit 2 ;;
	esac
	shift
done

PERSIST="${PERSIST:-0}"

fetch() {
	curl -fsS --max-time 20 "$1"
}

if [ "$MODE" = "check" ]; then
	echo "== IPv4 CF-ALLOW =="
	iptables -S CF-ALLOW 2>/dev/null || echo "  （未安装）"
	echo "== IPv4 DOCKER-USER 跳转 =="
	iptables -S DOCKER-USER 2>/dev/null | grep -F 'CF-ALLOW' || echo "  （未安装）"
	if command -v ip6tables >/dev/null 2>&1; then
		echo "== IPv6 CF-ALLOW =="
		ip6tables -S CF-ALLOW 2>/dev/null || echo "  （未安装）"
	fi
	exit 0
fi

echo "拉取 Cloudflare 官方 IP 段…"
V4="$(fetch https://www.cloudflare.com/ips-v4)"
V6="$(fetch https://www.cloudflare.com/ips-v6)"

valid_cidr() {
	grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}/([0-9]|[12][0-9]|3[0-2])$|^[0-9a-fA-F:]+/([0-9]|[1-9][0-9]|1[01][0-9]|12[0-8])$' <<<"$1"
}

v4_count=0
while read -r cidr; do
	[ -n "$cidr" ] || continue
	valid_cidr "$cidr" || { echo "错误：$cidr 不像 CIDR，放弃（避免装半套规则）" >&2; exit 1; }
	v4_count=$((v4_count + 1))
done <<<"$V4"
v6_count=0
while read -r cidr; do
	[ -n "$cidr" ] || continue
	valid_cidr "$cidr" || { echo "错误：$cidr 不像 CIDR，放弃" >&2; exit 1; }
	v6_count=$((v6_count + 1))
done <<<"$V6"

if [ "$v4_count" -lt 5 ] || [ "$v6_count" -lt 3 ]; then
	echo "错误：IP 段数量异常（v4=$v4_count v6=$v6_count），可能是下载被劫持，放弃" >&2
	exit 1
fi
echo "拿到 IPv4 $v4_count 条、IPv6 $v6_count 条"

run() {
	if [ "$MODE" = "apply" ]; then "$@"; else echo "  $*"; fi
}

install_family() {
	local tool="$1"
	local label="$2"
	local ranges="$3"

	if [ "$MODE" = "apply" ] && ! command -v "$tool" >/dev/null 2>&1; then
		echo "错误：找不到 $tool，无法安装规则" >&2
		exit 1
	fi

	run "$tool" -N CF-ALLOW 2>/dev/null || true
	run "$tool" -F CF-ALLOW
	while read -r cidr; do
		[ -n "$cidr" ] || continue
		run "$tool" -A CF-ALLOW -s "$cidr" -j RETURN
	done <<<"$ranges"
	run "$tool" -A CF-ALLOW -j DROP

	if ! "$tool" -n -L DOCKER-USER >/dev/null 2>&1; then
		run "$tool" -N DOCKER-USER
	fi

	if [ "$MODE" = "apply" ]; then
		while "$tool" -C DOCKER-USER -p tcp -m multiport --dports "$PORTS" -j CF-ALLOW 2>/dev/null; do
			"$tool" -D DOCKER-USER -p tcp -m multiport --dports "$PORTS" -j CF-ALLOW
		done
		"$tool" -I DOCKER-USER 1 -p tcp -m multiport --dports "$PORTS" -j CF-ALLOW
	else
		echo "  $tool -I DOCKER-USER 1 -p tcp -m multiport --dports $PORTS -j CF-ALLOW"
	fi
	echo "$label：放行 Cloudflare，其余来源 $PORTS 一律 DROP"
}

install_family iptables IPv4 "$V4"
install_family ip6tables IPv6 "$V6"

if [ "$MODE" = "apply" ]; then
	if [ "$PERSIST" = "1" ]; then
		if command -v netfilter-persistent >/dev/null 2>&1; then
			netfilter-persistent save
			echo "已保存规则（netfilter-persistent）"
		else
			echo "提示：没装 netfilter-persistent，重启后规则会丢；加一条 @reboot 定时任务重跑本脚本" >&2
		fi
	fi
	echo
	echo "自检："
	iptables -S DOCKER-USER | grep -F 'CF-ALLOW' || true
	echo "非 Cloudflare 来源访问 80/443 应当超时；Cloudflare 边车访问应当正常。"
else
	echo
	echo "以上为预览，未做任何改动。确认后加 --apply。"
fi
