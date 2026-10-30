# ADR 0003：Cloudflare 使用本人账号

- 状态：待填写账号信息
- 日期：2026-10-30

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
