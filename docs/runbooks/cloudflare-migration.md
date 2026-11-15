# 迁到自己的 Cloudflare 账号

> 目标形态（2026-11-04 决定）：
>
> ```
> 访客 → Cloudflare（橙云，写 CF-Connecting-IP）→ Caddy :443（Origin CA 证书）
>        → api:8000 / go-api:8000 → db:5432 / uploads 卷
> ```
>
> 本文件覆盖：买域名 → 加 zone → 建 Token → 填配置 → 建 DNS 与证书 → 装防火墙
> → 演练 → 正式切流 → 回滚。与仓库里的占位 `example.com` 一一对应。

---

## 1. 买域名

推荐 **Cloudflare Registrar**（同一账号里直接注册，NS 自动就位，不用改注册商）：

1. 控制台左侧 **Domain Registration → Register Domains**
2. 搜域名 → 加入购物车 → 填地址与支付方式 → 注册

> - `.dev` 在 HSTS 预加载列表里，浏览器强制 HTTPS，正好符合本项目（`APP_COOKIE_SECURE=true`）。
> - 用别家注册商也可以，但要能改 NS；国内注册商的 `.cn` 还要实名，流程更长。
> - 备案只在大陆服务器上才需要。境外服务器不需要 ICP。
> - **大陆服务器 + 学校备案时**：域名要用学校（或法定代表人）名义注册，
>   TLD 必须可备案，别用 Cloudflare Registrar。详见 `deploy-cn.md` 第 4 节。

如果域名不是从 Cloudflare Registrar 买的：控制台 **Add a site** → 选 Free → 它会给你
两个 NS → 到注册商后台把 NS 改成这两个 → 等生效（几分钟到 24 小时）。

---

## 2. 找到需要的值

| 值 | 位置 | 形如 |
| --- | --- | --- |
| Account ID | 控制台首页右侧栏，或任意域名概览页右下 | 32 位十六进制 |
| Zone ID | 选中域名 → **Overview** 页右下「API」区块 | 32 位十六进制 |
| 域名 | 你自己定的，如 `challenge.example.com` | — |
| 源站 IP | 服务器公网 IPv4 | — |

Zone ID 也可以不填：`cf_provision.py` 会按域名自动查。Account ID 同理（从 zone 信息里取）。

---

## 3. 建 API Token

**My Profile（右上角头像）→ API Tokens → Create Token → Create Custom Token**

| 项 | 填什么 |
| --- | --- |
| Token name | `challenge-provision` |
| Permissions 1 | `Zone` → `Zone` → **Read** |
| Permissions 2 | `Zone` → `DNS` → **Edit** |
| Permissions 3 | `Zone` → `SSL and Certificates` → **Edit** |
| Zone Resources | **Include → Specific zone → 你的域名** |
| TTL | 留空（不过期），或设成上线后 30 天 |

创建后只显示一次，**直接粘进 `infra/.env.infra`，不要发到聊天里**：

```ini
DOMAIN=challenge.example.com
SERVER_IP=203.0.113.10
CLOUDFLARE_ACCOUNT_ID=（可留空）
CLOUDFLARE_ZONE_ID=（可留空）
CLOUDFLARE_API_TOKEN=（粘这里）
ACME_EMAIL=你的邮箱
TLS_MODE=origin
```

`infra/.env.infra` 已在 `.gitignore`，不会被提交。

---

## 4. 本机先干跑

```sh
python scripts/cf_provision.py check   # 只读：验证 Token、查 zone、列现有记录
python scripts/cf_provision.py plan    # 只读：打印 apply 会做什么
```

`check` 通过后会打印 Zone ID / Account ID，可以补进 `.env.infra` 免得每次都查。

---

## 5. 服务器准备

```sh
# 以 deploy 用户
sudo mkdir -p /srv && sudo chown deploy:deploy /srv
git clone <仓库地址> /srv/app && cd /srv/app
cp infra/.env.prod.example infra/.env.prod
# 填 DOMAIN / POSTGRES_PASSWORD / APP_BOT_CHECK_SECRET，并确认：
#   TLS_MODE=origin
#   APP_BEHIND_CLOUDFLARE=true
```

证书目录要存在（脚本会建，但先建好更不容易踩权限问题）：

```sh
mkdir -p infra/secrets/tls && chmod 700 infra/secrets/tls
```

---

## 6. 接入：DNS + Origin CA 证书

**在服务器上**跑（Token 只存在服务器，本机不用留）：

```sh
python3 scripts/cf_provision.py apply
```

它做三件事：

1. 把 `DOMAIN`（和可选 `--www`）建/改成 **A 记录 → `SERVER_IP`，橙云开启**；
2. 调 Cloudflare API 生成 **15 年期 Origin CA 证书**（域名 + `*.域名`），
   以 0600 写进 `infra/secrets/tls/origin.pem` / `origin.key`；
3. 打印下一步要填的环境变量。

如果第 2 步报接口不可用（不同账号/计划有差异），改用手动：

**SSL/TLS → Origin Server → Create Certificate** → 私钥类型 RSA → 主机名填
`你的域名` 和 `*.你的域名` → 有效期 15 年 → 把证书与私钥分别存成
`infra/secrets/tls/origin.pem` / `origin.key`，`chmod 600`。

---

## 7. 防火墙：只让 Cloudflare 进来

> ⚠️ Docker 发布端口走 FORWARD 链，**ufw 的 INPUT 规则拦不住**。必须用
> `DOCKER-USER`；`scripts/cf_firewall.sh` 已经按这个写了。

```sh
./scripts/cf_firewall.sh            # 先看它要装什么
sudo ./scripts/cf_firewall.sh --apply
sudo ./scripts/cf_firewall.sh --check
```

- 规则内容：建 `CF-ALLOW` 链，放行 Cloudflare 官方 v4/v6 段，其余 `DROP`，
  在 `DOCKER-USER` 最前面按端口跳过去。
- 只保护 80/443；SSH 走 INPUT，不受影响。
- 重启后规则不保留。`--persist` 会调 `netfilter-persistent save`；
  没装的话加一条 `@reboot` 定时任务重跑脚本。
- **共用机器注意**：这台 VPS 上的 Unscripted Caddy 也发布 80/443，
  在共享机上跑会把它的入站一起限制。演练阶段用
  `--ports 8443` 只保护演练端口，别动整台机器。

验证：

```sh
# 经 Cloudflare 访问：应当 200，且响应头有 cf-ray
curl -sI https://你的域名/api/v1/health | grep -i 'cf-ray\|HTTP/'
# 直连源站 IP：应当超时（证明 CF-Connecting-IP 不可能被伪造）
curl -sk --max-time 5 https://<服务器IP>/api/v1/health || echo "已拦截（符合预期）"
```

---

## 8. 演练：共用服务器上的 8443 方案

新服务器还没到位时，先在现有 VPS 上验证整条链路，且不碰 Unscripted 的 Caddy。

1. 这台机器上的 challenge compose 用非标准端口：

   ```ini
   # infra/.env.prod
   CADDY_HTTP_PORT=8080
   CADDY_HTTPS_PORT=8443
   ```

2. 起服务：

   ```sh
   docker compose -p challenge --env-file infra/.env.prod -f infra/docker-compose.prod.yml \
     up -d --no-build db api go-api caddy
   ```

3. 在 Cloudflare 加 **Origin Rule**（域名 → Rules → Origin Rules → Create rule）：

   | 项 | 值 |
   | --- | --- |
   | Rule name | `challenge drill` |
   | When | `Hostname equals 你的域名` |
   | Then | **Destination Port** → Rewrite to `8443` |

   > 账号里没有 Origin Rules 时，退回上次的做法：在 Unscripted 的 Caddy 里加一段
   > `handle_path /__challenge/*` 指向 challenge 容器（需要把 Caddy 接进
   > `challenge_default` 网络），测完删掉并 `caddy reload`。

4. 安全组/防火墙放行 **8443**（只对本机需要，CF 回源走公网 IP 也要放行）。

5. 验证 SSE：`curl -N https://你的域名/api/v1/votes/stream`，6 秒内应有 3 帧左右。

演练结束：`docker compose -p challenge ... down`（保留卷），删掉 Origin Rule。

---

## 9. 正式切流（新服务器）

1. 新服务器按第 5–7 步部署好，`CADDY_HTTP_PORT=80` / `CADDY_HTTPS_PORT=443`；
2. `python3 scripts/cf_provision.py apply` 把 A 记录改到新 IP；
3. 安全组放行 80/443；
4. 验证下面第 10 节的清单。

Cloudflare 控制台需要手动开的设置（都在域名下）：

| 位置 | 设置 | 值 |
| --- | --- | --- |
| SSL/TLS → Overview | 加密模式 | **Full (strict)** |
| SSL/TLS → Edge Certificates | Always Use HTTPS | 开 |
| SSL/TLS → Edge Certificates | Minimum TLS Version | 1.2 |
| SSL/TLS → Edge Certificates | Automatic HTTPS Rewrites | 开 |
| Speed → Optimization | Brotli | 开 |
| Network | HTTP/3 | 可以关（SSE 走 QUIC 时排障更麻烦） |
| Security → Bots | Bot Fight Mode | **建议关**（会拦脚本与监控探针） |

---

## 10. 切流后验证清单

- [ ] `curl -sI https://域名/api/v1/health` → 200，响应头有 `cf-ray`、`strict-transport-security`
- [ ] 证书链来自 Cloudflare 边缘（不是源站证书），`curl -vI` 看 issuer
- [ ] 直连源站 IP 的 80/443 超时（防火墙生效）
- [ ] Go / Python 分流仍按 `infra/Caddyfile` 的规则（`/events`、`/votes` → Go）
- [ ] SSE `/api/v1/votes/stream` 首帧 < 1 s，且 100 秒内不断流（CF 空闲上限 100 s，
      现有心跳 16 s 一次）
- [ ] 真实客户端 IP：把 `APP_RATE_LIMIT_RPS` 临时设成 `10`，用两个不同网络各发
      10 个请求，两边应各自独立计数（说明按真实访客 IP 分桶，而不是都记在 Caddy 上）
- [ ] 登录失败限流：同一邮箱错 11 次 → 429 + `Retry-After`
- [ ] 上传：小文件直传 + 分片合并 + 下载，均正常
- [ ] 邮件：注册验证码真实投递

---

## 11. 回滚

按影响从小到大：

1. **只回滚流量**：Cloudflare 里把该 A 记录改成 **灰云**（DNS only），
   访问直连源站；此时源站证书仍是 Origin CA，浏览器会报警，只适合应急排查。
2. **回滚到旧服务器**：`cf_provision.py apply` 前把 `SERVER_IP` 改回旧 IP。
   记录是 upsert，不新增记录。
3. **回滚 TLS 模式**：`infra/.env.prod` 改 `TLS_MODE=auto`，
   `docker compose up -d caddy`（Caddy 会去签 Let's Encrypt；要求 80 端口能从公网访问）。
4. **撤掉防火墙**：`sudo iptables -F CF-ALLOW && sudo iptables -D DOCKER-USER -j CF-ALLOW`
   （或按 `--check` 打出来的规则逐条删）。

---

## 12. 已知坑

- **Cloudflare 会拦非浏览器 UA**（演练时 `Python-urllib/*` 直接 403）。上线前决定：
  保留并在监控/脚本里用正常 UA，或者加一条 WAF skip 规则。`tests/parity/` 里的脚本
  默认就是普通 UA，不受影响。
- **Bot Fight Mode** 会误伤 API 客户端，别开。
- **SSE 与 gzip**：Caddy 会压缩 SSE 但逐帧 flush，实测正常；Cloudflare 的
  「Rocket Loader」不要开。
- **源站真实 IP 泄漏**：橙云只隐藏 DNS，历史 DNS 记录、证书透明度日志仍可能暴露源站。
  防火墙是最后一道，务必装。
- **Origin CA 证书续期**：15 年期，不需要续；但换域名就要重签。
- **多副本 / 多机器**：`cf_firewall.sh` 每台都要跑；`cf_provision.py` 只在
  改记录时跑一次。
