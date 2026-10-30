# Cloudflare 接入（使用你自己的账号）

本项目**不沿用**原 Unscripted 仓库的任何 Cloudflare 账号、Zone、域名或证书。
上线前需要把下面这些换成你自己的：

| 配置项 | 说明 | 放在哪 |
|---|---|---|
| `DOMAIN` | 你的主域名，例如 `your-domain.com` | `infra/.env.infra` |
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare 控制台 → 概览页右下角的 Account ID | `infra/.env.infra` |
| `CLOUDFLARE_ZONE_ID` | 该域名在 Cloudflare 的 Zone ID（概览页 API 区域） | `infra/.env.infra` |
| `CLOUDFLARE_API_TOKEN` | 至少需要 `Zone:Read`、`DNS:Edit`、`SSL and Certificates:Edit` 三个权限 | `infra/.env.infra`（只在本机/服务器，勿提交） |
| Origin 证书 | SSL/TLS → Origin Server → 生成证书，存为 `infra/secrets/tls/origin.pem` + `origin.key` | 服务器本地 |
| SSL 模式 | 设为 **Full (strict)** | Cloudflare 控制台 |

## 部署形态

```
访客 → Cloudflare（写入 CF-Connecting-IP）→ Caddy（TLS，来自 Origin CA）→ api / 静态前端
```

- 后端只有在 `APP_BEHIND_CLOUDFLARE=true` 且连接来自 `APP_TRUSTED_PROXIES` 中的 Cloudflare 网段时，
  才采信 `CF-Connecting-IP`（见 `backend/app/core/net.py`）。
- 80/443 只接受 Cloudflare 网段的连接；`scripts/cf-firewall.sh` 每天刷新一次地址表。
- SSE 心跳必须短于 Cloudflare 的 100 秒空闲上限。

## 待你提供

把上面五个值给我（API Token 建议单独给我一份临时 Token，或你自己写进服务器上的
`infra/.env.infra`），我再把 `infra/Caddyfile` 与防火墙脚本里的占位替换掉。
在你提供之前，仓库里一律是 `example.com` 占位，不会误指向任何既有账号。
