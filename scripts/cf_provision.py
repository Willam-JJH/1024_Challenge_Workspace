#!/usr/bin/env python3
"""把域名接入自己的 Cloudflare 账号：A 记录 + Origin CA 证书。

只依赖标准库，不引入额外依赖。凭据从 ``infra/.env.infra`` 读取
（同名环境变量优先）：

    DOMAIN                 要接入的域名（可以是子域，如 challenge.example.com）
    SERVER_IP              源站公网 IP
    CLOUDFLARE_API_TOKEN   需要 Zone:Read、DNS:Edit、SSL and Certificates:Edit
    CLOUDFLARE_ZONE_ID     可选，留空时按 DOMAIN 自动查
    CLOUDFLARE_ACCOUNT_ID  可选，留空时从 zone 信息里取

用法（在仓库根目录）：

    python scripts/cf_provision.py check     # 只读：验证 Token、查 zone、列现有记录
    python scripts/cf_provision.py plan      # 只读：打印 apply 将要做的改动
    python scripts/cf_provision.py apply     # 真正建/改 DNS，并生成 Origin CA 证书

    # 只做其中一件
    python scripts/cf_provision.py apply --dns-only
    python scripts/cf_provision.py apply --cert-only --force-cert

安全约定：

- 任何情况下都不打印 Token、私钥或证书正文；
- 私钥以 0600 写入 ``infra/secrets/tls/``（目录已在 .gitignore 里）；
- 默认是只读模式，必须显式写 ``apply`` 才会改远端。
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ENV_FILE = ROOT / "infra" / ".env.infra"
SECRET_DIR = ROOT / "infra" / "secrets" / "tls"
API_BASE = os.environ.get("CF_API_BASE", "https://api.cloudflare.com/client/v4").rstrip("/")

ORIGIN_CERT_DAYS = 5475  # Cloudflare Origin CA 最长 15 年


class CFError(RuntimeError):
    """Cloudflare API 返回的失败。消息里不会包含凭据。"""


def load_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    if not path.exists():
        return values
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def setting(env: dict[str, str], key: str, default: str = "") -> str:
    return os.environ.get(key) or env.get(key) or default


def _brief(text: str, limit: int = 400) -> str:
    text = " ".join(text.split())
    return text if len(text) <= limit else text[:limit] + "…"


def api(method: str, path: str, token: str, body: dict | None = None):
    url = f"{API_BASE}{path}"
    data = json.dumps(body).encode("utf-8") if body is not None else None
    request = urllib.request.Request(url, data=data, method=method)
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")
        raise CFError(f"{method} {path} → HTTP {exc.code}：{_brief(detail)}") from None
    except urllib.error.URLError as exc:
        raise CFError(f"连不上 Cloudflare（{exc.reason}），检查本机网络或代理") from None
    if not payload.get("success", False):
        raise CFError(f"{method} {path} 失败：{_brief(json.dumps(payload, ensure_ascii=False))}")
    return payload.get("result")


def verify_token(token: str) -> None:
    result = api("GET", "/user/tokens/verify", token)
    status = (result or {}).get("status")
    if status != "active":
        raise CFError(f"Token 状态是 {status!r}，不是 active")
    print("  Token：active")


def find_zone(token: str, domain: str, pinned_id: str = "") -> dict:
    if pinned_id:
        zone = api("GET", f"/zones/{pinned_id}", token)
        print(f"  Zone：{zone['name']}（{zone['id']}，凭 CLOUDFLARE_ZONE_ID 直接取）")
        if not domain.endswith(zone["name"]):
            raise CFError(f"DOMAIN={domain} 不属于 zone {zone['name']}")
        return zone

    # 从最长到最短逐级尝试，支持 DOMAIN 是子域（challenge.example.com）
    labels = domain.split(".")
    for index in range(len(labels) - 1):
        candidate = ".".join(labels[index:])
        zones = api("GET", f"/zones?name={candidate}&status=active", token) or []
        zones = [item for item in zones if item.get("name") == candidate]
        if zones:
            zone = zones[0]
            print(f"  Zone：{zone['name']}（{zone['id']}）")
            return zone
    raise CFError(
        f"账号里找不到 {domain} 对应的已激活 zone。"
        "先在 Cloudflare 控制台「添加站点」，并把域名的 NS 改成 Cloudflare 给的两个。"
    )


def record_type_of(address: str) -> str:
    return "AAAA" if ":" in address else "A"


def list_records(token: str, zone_id: str, name: str) -> list[dict]:
    query = f"/zones/{zone_id}/dns_records?name={name}&per_page=100"
    return api("GET", query, token) or []


def plan_record(token: str, zone_id: str, name: str, address: str) -> tuple[str, dict | None]:
    """返回 (动作, 现有记录)。动作是 create / update / noop。"""
    kind = record_type_of(address)
    for record in list_records(token, zone_id, name):
        if record["type"] != kind:
            continue
        if record["content"] == address and record.get("proxied") is True:
            return "noop", record
        return "update", record
    return "create", None


def apply_record(token: str, zone_id: str, name: str, address: str) -> str:
    action, record = plan_record(token, zone_id, name, address)
    body = {
        "type": record_type_of(address),
        "name": name,
        "content": address,
        "proxied": True,
        "ttl": 1,  # 1 = Auto；橙云要求 ttl=1
    }
    if action == "noop":
        return f"{name} 已是 {address}（橙云），跳过"
    if action == "update":
        api("PATCH", f"/zones/{zone_id}/dns_records/{record['id']}", token, body)
        return f"{name} 更新为 {address}（橙云）"
    api("POST", f"/zones/{zone_id}/dns_records", token, body)
    return f"{name} 新建 {address}（橙云）"


def create_origin_cert(token: str, account_id: str, domain: str) -> tuple[str, str, list[str]]:
    """生成 Origin CA 证书，返回 (证书 PEM, 私钥 PEM, 实际签发的域名列表)。"""
    attempts = [[domain, f"*.{domain}"], [domain]]
    last_error: CFError | None = None
    for hostnames in attempts:
        body = {
            "hostnames": hostnames,
            "requested_validity": ORIGIN_CERT_DAYS,
            "request_type": "origin-rsa",
        }
        for path in (f"/accounts/{account_id}/certificates", "/certificates"):
            try:
                result = api("POST", path, token, body)
            except CFError as exc:
                last_error = exc
                continue
            return result["certificate"], result["private_key"], hostnames
    assert last_error is not None
    raise last_error


def display(path: Path) -> str:
    """打印用路径：尽量相对仓库根，实在不在根下就原样输出。"""
    try:
        return str(path.relative_to(ROOT))
    except ValueError:
        return str(path)


def write_secret(path: Path, content: str) -> None:
    SECRET_DIR.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(content, encoding="utf-8")
    os.chmod(temporary, 0o600)
    temporary.replace(path)


def require(env: dict[str, str], key: str) -> str:
    value = setting(env, key)
    if not value or value.startswith("replace-me"):
        raise CFError(f"infra/.env.infra 里 {key} 还没填")
    return value


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="把域名接入自己的 Cloudflare 账号")
    parser.add_argument("command", choices=["check", "plan", "apply"])
    parser.add_argument("--dns-only", action="store_true", help="只处理 DNS 记录")
    parser.add_argument("--cert-only", action="store_true", help="只生成 Origin CA 证书")
    parser.add_argument("--www", action="store_true", help="同时把 www 指到同一台机器")
    parser.add_argument("--force-cert", action="store_true", help="覆盖已有证书文件")
    args = parser.parse_args(argv)

    env = load_env(ENV_FILE)
    try:
        domain = require(env, "DOMAIN")
        token = require(env, "CLOUDFLARE_API_TOKEN")
    except CFError as exc:
        print(f"✗ {exc}", file=sys.stderr)
        return 2

    address = setting(env, "SERVER_IP")
    zone_id = setting(env, "CLOUDFLARE_ZONE_ID")
    account_id = setting(env, "CLOUDFLARE_ACCOUNT_ID")
    do_dns = not args.cert_only
    do_cert = not args.dns_only

    print(f"域名：{domain}")
    try:
        verify_token(token)
        zone = find_zone(token, domain, zone_id)
        zone_id = zone["id"]
        account_id = account_id or zone.get("account", {}).get("id", "")
    except CFError as exc:
        print(f"✗ {exc}", file=sys.stderr)
        return 1

    if args.command == "check":
        print("\n现有记录：")
        for record in list_records(token, zone_id, domain):
            cloud = "橙云" if record.get("proxied") else "灰云"
            print(f"  {record['type']:5} {record['name']} → {record['content']}（{cloud}）")
        if not list_records(token, zone_id, domain):
            print("  （空）")
            print("\n把下面两行补进 infra/.env.infra：")
            print(f"  CLOUDFLARE_ZONE_ID={zone_id}")
            print(f"  CLOUDFLARE_ACCOUNT_ID={account_id}")
        return 0

    if do_dns:
        if not address:
            print("✗ infra/.env.infra 里 SERVER_IP 还没填", file=sys.stderr)
            return 2
        names = [domain] + ([f"www.{domain}"] if args.www else [])
        print("\nDNS：")
        for name in names:
            if args.command == "plan":
                action, record = plan_record(token, zone_id, name, address)
                current = "" if record is None else f"（当前 {record['content']}）"
                print(f"  {action:6} {name} → {address}，橙云{current}")
            else:
                print(f"  {apply_record(token, zone_id, name, address)}")

    if do_cert:
        certificate = SECRET_DIR / "origin.pem"
        private_key = SECRET_DIR / "origin.key"
        print("\nOrigin CA 证书：")
        if certificate.exists() and private_key.exists() and not args.force_cert:
            print(f"  已存在 {display(certificate)}，跳过（--force-cert 覆盖）")
        elif not account_id:
            print("  ✗ 拿不到 Account ID，先在 .env.infra 里填上", file=sys.stderr)
            return 2
        elif args.command == "plan":
            print(f"  将生成 {ORIGIN_CERT_DAYS // 365} 年期证书，写入 infra/secrets/tls/")
        else:
            try:
                pem, key, hostnames = create_origin_cert(token, account_id, domain)
            except CFError as exc:
                print(f"  ✗ {exc}", file=sys.stderr)
                print(
                    "  若接口不可用，改在控制台 SSL/TLS → Origin Server → Create Certificate\n"
                    "  生成 15 年期证书（域名 + *.域名），把 origin.pem / origin.key 放进\n"
                    f"  {display(SECRET_DIR)}，权限 600。",
                    file=sys.stderr,
                )
                return 1
            write_secret(certificate, pem)
            write_secret(private_key, key)
            print(f"  已写入 {display(certificate)} / {display(private_key)}（权限 600）")
            print(f"  签发域名：{', '.join(hostnames)}")

    if args.command == "plan":
        print("\n这是只读预览；确认无误后把 plan 换成 apply。")
    else:
        print("\n下一步：infra/.env.prod 里设置")
        print(f"  DOMAIN={domain}")
        print("  TLS_MODE=origin")
        print("  APP_BEHIND_CLOUDFLARE=true")
        print("然后在服务器上跑 scripts/cf_firewall.sh --apply。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
