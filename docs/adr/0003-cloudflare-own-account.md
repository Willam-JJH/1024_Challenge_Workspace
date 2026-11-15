# ADR 0003：Cloudflare 使用本人账号

- 状态：方案已定，待域名与 Token
- 日期：2026-10-30（2026-11-04 补：接入方式与工具链）

## 决策补充（2026-11-04）

- 接入方式：**橙云 + Origin CA 证书**（Full strict），不用 Tunnel、不用灰云。
- 域名：尚未购买；购买后加入本人 Cloudflare 账号（推荐 Cloudflare Registrar）。
- 自动化：`scripts/cf_provision.py`（DNS + 证书，默认只读）与
  `scripts/cf_firewall.sh`（DOCKER-USER 只放行 Cloudflare 网段）。
- TLS 由 `infra/caddy/tls-origin.caddy` 提供，compose 用 `TLS_MODE` 选择；
  `TLS_MODE=auto` 保留原来的 Let's Encrypt 路径。
- 完整步骤见 `docs/runbooks/cloudflare-migration.md`。

## 背景

原仓库的基础设施绑定的是原作者的域名与 Cloudflare 账号（Origin CA 证书、IP 段防火墙、
`platform.scie1024.dev` 等）。新仓库不能沿用。

## 决策

- 所有域名一律使用占位 `example.com`，不出现任何既有域名。
- 账号、Zone、API Token 通过 `infra/.env.infra` 注入，不写入仓库。
- 后端只在 `APP_BEHIND_CLOUDFLARE=true` 且对端在 `APP_TRUSTED_PROXIES` 网段内时采信
  `CF-Connecting-IP`。

## 待办

需要本人提供：域名、Cloudflare Account ID、Zone ID、API Token（权限见
`infra/cloudflare/README.md`），以及是否使用 Origin CA 证书。填好后再补 Caddy 的 TLS 配置与
防火墙脚本。
