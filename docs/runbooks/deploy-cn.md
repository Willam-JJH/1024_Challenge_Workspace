# 国内服务器部署（阿里云轻量 + Caddy）

目标形态：一台大陆服务器跑 Docker Compose（Postgres + API + Caddy），域名已备案，
Caddy 自动申请 Let's Encrypt 证书，不使用 Cloudflare 代理。

```
访客 → Caddy :443（自动 HTTPS）→ api:8000（FastAPI）→ db:5432（Postgres）
                                        ↘ uploads 卷（素材）
```

---

## 0. 前置条件

- [ ] 域名已在**国内注册商**购买并完成**实名认证**
- [ ] 服务器为**大陆地域**，包年包月 ≥ 3 个月（才能申请备案服务码）
- [ ] 已完成 **ICP 备案**，拿到备案号（备案期间网站不能对外开放）
- [ ] 安全组放行 **22 / 80 / 443**（其余全部关闭）
- [ ] 系统为 Ubuntu 24.04 LTS，有公网 IPv4

> 备案通过前不要把域名解析到这台服务器；先做下面第 1–2 步的准备即可。

---

## 1. 服务器初始化

```bash
# 以 root 登录后
adduser deploy && usermod -aG sudo deploy
# 把本机公钥放进 /home/deploy/.ssh/authorized_keys，然后测试：
#   ssh deploy@<服务器IP>

timedatectl set-timezone Asia/Shanghai
apt update && apt upgrade -y
```

安装 Docker 与 Compose 插件：

```bash
curl -fsSL https://get.docker.com | sh
usermod -aG docker deploy
# 重新登录让组权限生效
docker --version && docker compose version
```

---

## 2. 拉代码与配置

```bash
sudo mkdir -p /srv && sudo chown deploy:deploy /srv
git clone https://github.com/Willam-JJH/1024_Challenge_Workspace.git /srv/app
cd /srv/app

# 生产环境变量（不要提交）
cp infra/.env.prod.example infra/.env.prod
openssl rand -base64 32   # 生成 POSTGRES_PASSWORD
python3 -c "import secrets;print(secrets.token_urlsafe(32))"   # 生成 APP_BOT_CHECK_SECRET
nano infra/.env.prod      # 填 DOMAIN / 两个随机串
```

`infra/.env.prod` 需要填的只有四项：

| 键 | 值 |
|---|---|
| `DOMAIN` | 你的域名，如 `example.com` |
| `POSTGRES_PASSWORD` | 上面生成的长随机串 |
| `POSTGRES_USER` / `POSTGRES_DB` | 默认 `app` / `challenge`，可不动 |
| `APP_BOT_CHECK_SECRET` | 上面生成的随机串 |

---

## 3. 首次启动

```bash
cd /srv/app

# 1) 只起数据库
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod up -d db

# 2) 建表（Alembic）
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod \
  run --rm api alembic upgrade head

# 3) 创建第一个管理员
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod \
  run --rm api python scripts/create_admin.py --email you@example.com --name 你的名字

# 4) 起全部服务
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod up -d
```

服务状态与日志：

```bash
docker compose -f infra/docker-compose.prod.yml ps
docker compose -f infra/docker-compose.prod.yml logs -f api
docker compose -f infra/docker-compose.prod.yml logs -f caddy
```

---

## 4. 解析与证书

备案通过后：

1. 域名解析：`A` 记录 → 服务器公网 IP（TTL 600）
2. 等 DNS 生效：`dig +short 你的域名`
3. Caddy 会自动申请证书（首次约 10 秒）；看日志里的 `certificate obtained successfully`
4. 验证：

```bash
curl -s https://你的域名/api/v1/health
# {"status":"ok","version":"0.1.0","database":"ok"}

curl -s https://你的域名/.well-known/oauth-authorization-server
```

---

## 5. 备案与合规

- [ ] 网站页脚展示备案号，并链接到 `https://beian.miit.gov.cn/`
      （前端重设计后加；在那之前至少确认访问页有备案号）
- [ ] 备案通过后 **30 天内**完成**公安联网备案**
      （全国互联网安全管理服务平台，拿到公网安备号后同样放页脚）
- [ ] 备案信息变更（换服务器、换域名、换主体）要在 30 日内提交变更
- [ ] 未备案域名解析到大陆服务器会被拦截，不要提前解析

---

## 6. 备份

数据库与素材每天各备份一次，保留 14 天：

```bash
sudo mkdir -p /srv/backups && sudo chown deploy:deploy /srv/backups
crontab -e
```

```cron
30 3 * * * cd /srv/app && docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod exec -T db pg_dump -U app challenge | gzip > /srv/backups/db-$(date +\%F).sql.gz
40 3 * * * cd /srv/app && docker run --rm -v app_uploads:/data -v /srv/backups:/backup alpine tar czf /backup/uploads-$(date +\%F).tar.gz -C /data .
50 3 * * * find /srv/backups -name "*.gz" -mtime +14 -delete
```

恢复：

```bash
gunzip -c /srv/backups/db-2026-11-01.sql.gz | \
  docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod exec -T db psql -U app challenge
```

> 卷名以 `docker volume ls` 为准（通常是 `app_uploads`，前缀是目录名）。

---

## 7. 升级与回滚

```bash
cd /srv/app
git pull
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod build api
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod \
  run --rm api alembic upgrade head
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod up -d api caddy
```

回滚代码：

```bash
git checkout <上一个 release 的 commit>
# 迁移不回滚；如果这次迁移不可逆，先恢复第 6 步的数据库备份
docker compose ... build api && docker compose ... up -d api
```

---

## 8. 常见故障

| 现象 | 排查 |
|---|---|
| 443 拿不到证书 | 80 端口是否被安全组放行；域名是否已解析；备案未通过时证书能签但访问会被拦 |
| `502` | `docker compose logs api`：多半是数据库没起来或迁移没跑 |
| 登录后立刻掉线 | `APP_COOKIE_SECURE=true` 时必须是 https 访问 |
| 上传失败 | `uploads` 卷是否挂载；`APP_UPLOAD_DIR=/data/uploads` |
| 磁盘涨满 | 日志已限制 10 MB×3；检查 `/srv/backups` 与上传目录 |
| 时间不对 | 容器默认 UTC；数据库存 UTC 是设计如此，展示在前端转换 |

---

## 9. 待填清单（等买完服务器/域名）

- [ ] `DOMAIN` → `infra/.env.prod`
- [ ] 备案号 → 前端页脚
- [ ] 公网安备号 → 前端页脚
- [ ] 是否启用 Cloudflare：走本 runbook 不需要；如只做 DNS（灰云）再填 `CLOUDFLARE_ZONE_ID`
