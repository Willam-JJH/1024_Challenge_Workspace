# 阿里云大陆服务器部署（ECS + Caddy）

> **自建迷你主机方案见 [`deploy-selfhost.md`](deploy-selfhost.md)**：
> 家里放机器 + Cloudflare Tunnel，成本更低但无法备案。
> 本文是云服务器路线，两条线并存，按是否需要 ICP 备案选择。

目标形态有两种，TLS 模式由 `TLS_MODE` 切换：

```
# 橙云（默认，WAF + 隐藏源站 IP，大陆访问多 100–200 ms）
访客 → Cloudflare → Caddy :443（Origin CA 证书）→ api / go-api → db / uploads

# 灰云（最快，源站 IP 暴露，无 WAF）
访客 → Caddy :443（Let's Encrypt 自动签发）→ api / go-api → db / uploads
```

Cloudflare 侧的 DNS、证书、防火墙脚本见 `cloudflare-migration.md`；
本文负责阿里云这边的机器。

---

## 0. 前置条件

- [ ] 阿里云账号已完成**实名认证**
- [ ] 域名在**国内注册商**购买（阿里云万网最省事），并完成**实名认证**
- [ ] 域名的 TLD **可备案**：`.com` / `.cn` / `.net` 一定可以；
      `.dev` 等新 gTLD 要在备案系统里填一遍确认，别先买再试
- [ ] **域名持有者与备案主体一致**：走学校备案时，域名要以学校（或法定代表人）
      名义注册，个人名义买的域名不能直接挂在学校主体下
- [ ] ECS 为**包年包月且时长 ≥ 3 个月**（按量付费申请不到备案服务码）
- [ ] 已完成 **ICP 备案**并拿到备案号（备案期间网站不能对外开放）
- [ ] 系统为 Ubuntu 24.04 LTS

## 0.1 ECS 选购

| 项 | 选什么 |
| --- | --- |
| 实例规格 | **通用型 g8i / g7，2 vCPU 8 GiB**；预算紧用计算型 c8i / c7 的 2C4G |
| 系统盘 | ESSD PL0 40 GiB |
| 数据盘 | 建议 100 GiB ESSD，放上传素材与备份 |
| 带宽 | 固定 5 Mbps 起步，随时可升 |
| 安全组 | 只开 22（来源填自己的固定 IP） |

**不要选**：经济型 e、突发性能 t6、共享型。这几类靠 CPU 积分运行，
持续压测时会被限到基线以下，前面测出来的容量数字不适用。

> 5 Mbps 约 0.6 MB/s，素材上传会慢。赛事有大文件时改成「按使用流量 + 峰值 50–100M」，
> 按实际流量付费（大陆约 ¥0.8/GB），记得盯账单。

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

### 1.1 Docker 镜像加速（大陆必配）

大陆直连 Docker Hub 很慢，基础镜像（`golang`、`python`、`postgres`、`caddy`）
经常拉不下来。到 **容器镜像服务 ACR → 镜像工具 → 镜像加速器** 复制专属地址：

```bash
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json >/dev/null <<'EOF'
{
  "registry-mirrors": ["https://<你的ID>.mirror.aliyuncs.com"]
}
EOF
sudo systemctl restart docker
docker info | grep -A2 "Registry Mirrors"
```

如果加速器仍然拉不到某个镜像，退路是用 ACR 个人版免费仓库中转：
在能访问 Docker Hub 的机器上 `docker pull` → `docker tag` → `docker push` 到 ACR，
服务器再从 ACR 拉。`infra/docker-compose.prod.yml` 里的 `image:` 改成 ACR 地址即可。

---

## 2. 拉代码与配置

```bash
sudo mkdir -p /srv && sudo chown deploy:deploy /srv
git clone <仓库地址> /srv/app && cd /srv/app
cp infra/.env.prod.example infra/.env.prod
```

`infra/.env.prod` 里必须确认的：

| 键 | 值 |
| --- | --- |
| `DOMAIN` | 域名，如 `challenge.example.com` |
| `POSTGRES_PASSWORD` | `openssl rand -base64 32` 生成 |
| `APP_BOT_CHECK_SECRET` | `python3 -c "import secrets;print(secrets.token_urlsafe(32))"` |
| `TLS_MODE` | 橙云 `origin`；灰云 `auto` |
| `APP_BEHIND_CLOUDFLARE` | 橙云 `true`；灰云 `false` |

证书目录（橙云用）：

```bash
mkdir -p infra/secrets/tls && chmod 700 infra/secrets/tls
```

---

## 3. 首次启动

```bash
cd /srv/app
COMPOSE="docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod"

# 1) 只起数据库
$COMPOSE up -d db
# 2) 建表
$COMPOSE run --rm api alembic upgrade head
# 3) 建第一个管理员
$COMPOSE run --rm api python scripts/create_admin.py --email you@example.com --name 你的名字
# 4) 起全部服务
$COMPOSE up -d
```

> 备案没通过前不要开 80/443：安全组先只放 22，Caddy 起来也访问不到。

---

## 4. 备案（学校主体）

决定：**以学校作为主办单位备案**（比个人备案范围宽，不会因「用户注册/投票/社区」
被要求转主体）。这里以深国交为例，其他学校流程相同。

### 4.1 材料

| 材料 | 说明 |
| --- | --- |
| 主办单位证件 | 民办非企业单位登记证书 / 事业单位法人证书 / 营业执照（用学校实际持有的那种） |
| 法定代表人身份证 | 正反面 |
| 网站负责人身份证 | 通常是你本人，需人脸核验 |
| 授权书 | 网站负责人不是法定代表人时必须提供，**加盖学校公章** |
| 备案信息真实性核验单 | 阿里云生成，**加盖学校公章** |
| 域名证书 | 域名持有者须为主办单位或法定代表人 |
| 备案服务码 | ECS 控制台 → 实例 → 备案 → 生成 |

### 4.2 流程

1. 阿里云备案系统 → 新增备案 → 主体选「单位」→ 填写学校信息
2. 填网站信息：域名、网站名称、服务内容
3. 上传材料、盖章件，阿里云 App 人脸核验
4. 阿里云初审 1–2 天 → 管局审核 7–15 个工作日

### 4.3 两个容易卡住的点

- **前置审批**：教育类内容在管局可能触发前置审批（需要教育主管部门文件）。
  按实际情况把服务内容写成「校园赛事活动信息展示与报名」，不要写成
  「教育培训 / 在线教育」——这是事实描述，不是规避。
- **域名持有者**：域名必须在学校或法定代表人名下。个人名下买的域名，
  要么过户，要么重新以学校名义注册。

### 4.4 通过后

- [ ] 网站页脚展示备案号，链接 `https://beian.miit.gov.cn/`
- [ ] **30 天内**完成**公安联网备案**
- [ ] 备案信息变更（换服务器/换域名/换主体）30 日内提交变更
- [ ] 明确一件事：备案主体是学校，网站对外代表学校，内容和运营要经学校同意

---

## 5. 解析与证书

### 橙云（推荐，`TLS_MODE=origin`）

按 `cloudflare-migration.md` 的第 1–7 步做：域名加进 Cloudflare → 建 API Token
→ `scripts/cf_provision.py apply` 建 A 记录并生成 Origin CA 证书
→ `scripts/cf_firewall.sh --apply` 只放行 Cloudflare 网段。

> 大陆机器访问 `api.cloudflare.com` 可能不稳，**建议在本机跑 `cf_provision.py`**，
> 生成后把 `infra/secrets/tls/` 两个文件传到服务器。

### 灰云（`TLS_MODE=auto`）

1. 阿里云 DNS 加 `A` 记录 → 服务器公网 IP，**关闭「CDN 加速」**
2. 安全组放行 80/443（来源 `0.0.0.0/0`）
3. `TLS_MODE=auto`，`ACME_EMAIL` 填邮箱
4. Caddy 自动申请 Let's Encrypt，首次约 10 秒；日志里找
   `certificate obtained successfully`

验证：

```bash
curl -s https://你的域名/api/v1/health
# {"status":"ok","version":"0.1.0","database":"ok"}
curl -s https://你的域名/.well-known/oauth-authorization-server
```

---

## 6. 安全组

| 方向 | 端口 | 来源 | 说明 |
| --- | --- | --- | --- |
| 入 | 22 | 自己的固定 IP | 不要开 0.0.0.0/0 |
| 入 | 80 / 443 | **Cloudflare 官方网段** | 15 条 IPv4 + 7 条 IPv6 |
| 出 | 全部 | 0.0.0.0/0 | 默认 |

- 安全组入方向默认上限 100 条，Cloudflare 一共 22 条，够用。
- Cloudflare 的网段会变：以 <https://www.cloudflare.com/ips/> 为准，
  用 `scripts/cf_firewall.sh` 在机器里再兜一层（`DOCKER-USER` 链），
  这样即使安全组放宽了也不会被直连绕过。
- 实例没有 IPv6 时只配 IPv4 那 15 条。

---

## 7. 邮件

阿里云大陆机器**出方向 25 端口默认封禁**，Gmail 的 465/587 也不通。
两条可用路径：

### 7.1 阿里云邮件推送（DirectMail）

1. 控制台开通「邮件推送」→ 创建**发信域名**（如 `mail.你的域名`）
2. 按提示在 DNS 加 TXT / MX 记录验证域名
3. 创建发信地址，拿到 SMTP 用户名/密码
4. 配置：

```ini
APP_MAIL_DRIVER=smtp
APP_MAIL_FROM=no-reply@mail.你的域名
APP_SMTP_HOST=smtpdm.aliyun.com
APP_SMTP_PORT=465
APP_SMTP_TLS=ssl
APP_SMTP_USER=发信地址
APP_SMTP_PASSWORD=SMTP 密码
```

> 注意：DirectMail 对验证码/通知类邮件可能要求**模板报备**，广播类邮件
> 要走批量发送。开通后先用 `tools/mailprobe` 实测一封，再跑
> `tests/parity/mail_flows.py`。

### 7.2 QQ / 163 企业邮箱授权码

465 端口通常可达，配置同理，`APP_SMTP_HOST=smtp.qq.com` /
`smtp.163.com`。免费邮箱有每日收件人配额（数百封/天），只适合验证码与邀请。

---

## 8. 备份

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

**异地备份**：系统盘或数据盘挂了本地备份一起没。用 OSS 再存一份：

```bash
# aliyun-cli 或 ossutil，示例用 ossutil
ossutil cp -r /srv/backups oss://<bucket>/challenge/$(date +%F)/ \
  --update --include "*.gz"
```

恢复：

```bash
gunzip -c /srv/backups/db-2026-11-01.sql.gz | \
  docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod exec -T db psql -U app challenge
```

> 卷名以 `docker volume ls` 为准（通常是 `app_uploads`，前缀是目录名）。

---

## 9. 升级与回滚

```bash
cd /srv/app
git pull
COMPOSE="docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod"
$COMPOSE build api go-api
$COMPOSE run --rm api alembic upgrade head
$COMPOSE up -d api go-api caddy
```

回滚代码：

```bash
git checkout <上一个 release 的 commit>
# 迁移不回滚；如果这次迁移不可逆，先恢复第 8 步的数据库备份
$COMPOSE build api go-api && $COMPOSE up -d api go-api
```

---

## 10. 常见故障

| 现象 | 排查 |
| --- | --- |
| 80/443 打不开 | 安全组是否放行；备案是否通过；`TLS_MODE` 与证书是否匹配 |
| 443 拿不到证书（灰云） | 80 端口是否对公网开放；域名是否已解析；备案未过时证书能签但访问会被拦 |
| `502` | `docker compose logs api`：多半是数据库没起来或迁移没跑 |
| Caddy 起不来 | `docker compose logs caddy`：`TLS_MODE=origin` 时证书文件不存在会直接退出 |
| 登录后立刻掉线 | `APP_COOKIE_SECURE=true` 时必须是 https 访问 |
| 上传失败 | `uploads` 卷是否挂载；`APP_UPLOAD_DIR=/data/uploads` |
| 拉镜像超时 | 第 1.1 节的加速器；仍不行走 ACR 中转 |
| 邮件发不出 | 大陆 25 端口封禁；确认用 465 且是 DirectMail 或 QQ/163 |
| 磁盘涨满 | 日志已限制 10 MB×3；检查 `/srv/backups`、上传目录、Docker 镜像 |
| 时间不对 | 容器默认 UTC；数据库存 UTC 是设计如此，展示在前端转换 |

---

## 11. 待填清单

- [ ] 学校主体证件（民办非企业单位登记证书 / 事业单位法人证书 / 营业执照）
- [ ] 学校负责盖章的部门与对接人（授权书、真实性核验单）
- [ ] 域名（可备案 TLD，持有者为学校或法定代表人）
- [ ] 备案号 → 前端页脚
- [ ] 公网安备号 → 前端页脚
- [ ] `TLS_MODE`：橙云 `origin` / 灰云 `auto`
- [ ] 邮件通道：DirectMail 或 QQ/163
- [ ] OSS bucket（异地备份）
