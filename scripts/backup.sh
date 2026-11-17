#!/usr/bin/env bash
# 每日备份：数据库逻辑备份 + 素材卷 + 运行时配置，全部进 restic。
#
# 安装、初始化与恢复演练见 docs/runbooks/deploy-selfhost.md §9。
# 建议放进 cron（root）：
#   30 3 * * * /srv/app/scripts/backup.sh >> /var/log/challenge-backup.log 2>&1
#
# 所有路径都可以用环境变量覆盖，默认值对应文档里的目录布局。
set -euo pipefail
umask 077

APP_DIR="${APP_DIR:-/srv/app}"
COMPOSE_FILE="${COMPOSE_FILE:-$APP_DIR/infra/docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-$APP_DIR/infra/.env.prod}"
RUSTDESK_DIR="${RUSTDESK_DIR:-/srv/rustdesk/data}"
HOST_TAG="${HOST_TAG:-challenge}"
LOCK_FILE="${LOCK_FILE:-/var/lock/challenge-backup.lock}"
# 可选：设置后把 restic 仓库再同步一份到对象存储（异地）
OSSUTIL_BUCKET="${OSSUTIL_BUCKET:-}"

export RESTIC_REPOSITORY="${RESTIC_REPOSITORY:-/backup/restic}"
export RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-/etc/restic/password}"

log() { printf '%s %s\n' "$(date -Is)" "$*"; }

# 同一时间只跑一份：重叠运行会让 prune 与 backup 抢仓库锁
exec 9>"$LOCK_FILE"
flock -n 9 || { log "已有备份在运行，本次退出"; exit 0; }

command -v restic >/dev/null || { log "缺少 restic"; exit 1; }
[[ -f "$RESTIC_PASSWORD_FILE" ]] || { log "缺少 $RESTIC_PASSWORD_FILE"; exit 1; }
[[ -f "$ENV_FILE" ]] || { log "缺少 $ENV_FILE"; exit 1; }
restic cat config >/dev/null 2>&1 || {
	log "restic 仓库不可用或未初始化：$RESTIC_REPOSITORY"
	exit 1
}

compose() { docker compose -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }

compose ps --status running --services | grep -qx db || {
	log "数据库容器没在运行，跳过本次备份"
	exit 1
}

log "开始备份"

# 1) 数据库：custom 格式，可直接 pg_restore；流式写进 restic，宿主机上不落临时文件
compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' |
	restic backup --stdin --stdin-filename challenge.dump --tag db --host "$HOST_TAG"
log "数据库已备份"

# 2) 素材：用容器里的挂载点定位真实路径，不依赖 compose 项目名
GO_API_ID="$(compose ps -q go-api || true)"
if [[ -z "$GO_API_ID" ]]; then
	log "go-api 没有运行，无法定位素材卷"
	exit 1
fi
UPLOADS_DIR="$(docker inspect -f \
	'{{ range .Mounts }}{{ if eq .Destination "/data/uploads" }}{{ .Source }}{{ end }}{{ end }}' \
	"$GO_API_ID")"
if [[ -z "$UPLOADS_DIR" || ! -d "$UPLOADS_DIR" ]]; then
	log "找不到素材卷挂载点（期望 /data/uploads）"
	exit 1
fi
restic backup "$UPLOADS_DIR" --tag uploads --host "$HOST_TAG"
log "素材已备份：$UPLOADS_DIR"

# 3) 运行时配置：没有 .env.prod 就等于丢了数据库口令与隧道 Token
restic backup "$ENV_FILE" "$COMPOSE_FILE" "$APP_DIR/infra/Caddyfile" \
	--tag config --host "$HOST_TAG"
log "配置已备份"

# 4) RustDesk 服务端密钥（装过才备份；丢了要重建整个远程访问网络）
if [[ -d "$RUSTDESK_DIR" ]]; then
	restic backup "$RUSTDESK_DIR" --tag rustdesk --host "$HOST_TAG"
	log "RustDesk 已备份"
fi

# 5) 保留策略：近 7 天每天一份、近 4 周每周一份、近 6 个月每月一份
restic forget --host "$HOST_TAG" \
	--keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune

# 6) 异地：restic 仓库整体同步到对象存储
if [[ -n "$OSSUTIL_BUCKET" ]]; then
	command -v ossutil >/dev/null || {
		log "设置了 OSSUTIL_BUCKET 但没装 ossutil"
		exit 1
	}
	ossutil sync "$RESTIC_REPOSITORY" "$OSSUTIL_BUCKET" --delete --update
	log "已同步到 $OSSUTIL_BUCKET"
fi

log "备份完成"
