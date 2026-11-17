# 自建迷你主机部署（家里 + Cloudflare Tunnel）

> 状态：方案 2026-11-05 定稿；**仓库侧文件 2026-11-17 已备好**（第 6 节），
> 硬件到货后按第 7 节直接部署，不需要再改代码。
>
> 云服务器路线（含 ICP 备案）仍保留在 `deploy-cn.md`，作为备选或正式对外
> 开放时的方案。

---

## 0. 决策摘要

| 项 | 决定 |
| --- | --- |
| 形态 | 自建迷你主机，放在家里 |
| 入口 | **Cloudflare Tunnel**（出站连接，不开入站端口） |
| 后端 | **纯 Go 端**上线，Python 不常驻（回滚时临时起） |
| 主机 | 迷你主机 **16 GB 内存 / 512 GB 系统盘** |
| 外接盘 | **1 TB 移动 SSD**，挂 `/backup` |
| 供电 | 配 UPS，断电时让 Postgres 安全落盘 |
| 备份 | **restic** 快照 + 保留策略（不是每日全量 tar） |
| 素材 | 只存最终展示内容；参赛者自行备份代码与文件 |
| 视频 | 保留原始分辨率，码率可压；平台不做服务器转码 |
| 直播 | 推 **B 站 + YouTube**（OBS 多路推流），费用 ¥0 |
| 回放 | 存 **YouTube / B 站**，网站只做嵌入播放 |
| 远程运维 | **RustDesk 自建**（hbbs + hbbr），与网站共用这台主机 |

### 为什么不是云服务器

- 硬件成本：一次性 ¥3,470，之后每年电费 + OSS 约 ¥300
- 云服务器 2C4G 约 ¥3,000/年，性能还更低
- 代价：没有 ICP 备案（家里宽带办不下来）、断电断网自己扛、无 SLA

**如果这一年要正式对外（不只是校内内测），备案这条路仍需回到学校机房
或云服务器。** 本文的方案在"能跑、便宜、快"三者上是优解，在"合规"上不是。

---

## 1. 目标形态

```
访客
  │
  ▼
Cloudflare（TLS 终结、WAF、缓存静态资源）
  │  cloudflared 主动出站长连接（443）
  ▼
迷你主机
  ├── cloudflared
  ├── Caddy :80（纯路由，无证书）
  ├── go-api :8000
  ├── db :5432
  │   └── uploads 卷（系统盘）
  └── hbbs :21116 / hbbr :21117（RustDesk，走端口转发）

备份：
  每日 restic 快照 ─▶ /backup（外接 1 TB SSD）
                    └▶ ossutil sync ─▶ 阿里云 OSS（异地）

直播 / 回放（不经过主机）：
  现场 OBS ─┬▶ B 站
            └▶ YouTube
  网站只嵌入 iframe 播放器
```

---

## 2. 硬件清单

| 项 | 规格 | 参考价 |
| --- | --- | --- |
| 迷你主机 | R7 8845HS / **16 GB** / **512 GB** | ¥2,400 |
| 外接 SSD | 1 TB 移动固态（有旧盘则 ¥0） | ¥450 |
| UPS | 山特 TG-BOX 850 / APC BK650M2-CH | ¥600 |
| 网线 | 六类，3–5 m | ¥20 |
| **合计** | | **≈ ¥3,470** |

选型要点：

- 内存 **16 GB 够**：四个容器实测 < 1.2 GB
- 系统盘 **512 GB 够**：系统 + 镜像 + 数据 + 素材约 45–90 GB
- 再加 RustDesk 的 hbbs / hbbr 只多 **约 300 MB 内存、100 MB 磁盘**，不影响选型
- 外接盘用 **SSD 不用机械盘**：7×24 挂着，机械盘会休眠、怕震动
- 迷你主机要确认 **DDR5 SODIMM 可插拔**（不是板载），将来能加内存
- 网口接有线，不用 Wi-Fi

> 不要买 N100/N305 小主机：4 个 E 核，压测余量和 SSE 并发都紧。

### 每年运行成本

| 项 | 金额 |
| --- | --- |
| 电费（平均 25 W） | ≈ ¥130 |
| OSS 异地备份（100 GB） | ≈ ¥144 |
| **合计** | **≈ ¥275/年** |

---

## 3. 与云服务器方案的差异

| 项 | 云 + 橙云（`deploy-cn.md`） | 自建 + Tunnel（本文） |
| --- | --- | --- |
| 公网入口 | A 记录 → 服务器 IP | Tunnel，无入站端口 |
| 证书 | Origin CA（`TLS_MODE=origin`） | Cloudflare 边缘证书 |
| Caddy 监听 | :443 + 证书 | **:80，纯 HTTP** |
| `cf_provision.py` | 建 A 记录 + 签发证书 | **不用**（Tunnel 自动生成 CNAME） |
| `cf_firewall.sh` | `DOCKER-USER` 只放行 CF 网段 | **不用**（没有入站端口） |
| 安全组 | 阿里云安全组 | 本机 `ufw`：22 + RustDesk 的 21115–21117 |
| RustDesk 服务端 | 不在这台机器上 | 同机跑 hbbs / hbbr，需额外入站端口（见 4.5） |
| `APP_BEHIND_CLOUDFLARE` | true | **true**（Tunnel 同样透传 `CF-Connecting-IP`） |
| 备案 | 必须（大陆机房） | 无法办理（家庭宽带） |
| 断电 | 机房 UPS | 自配 UPS |
| 硬件故障 | 提工单换机器 | 自己修，靠备份 |

> 保留了 `scripts/cf_provision.py` 与 `scripts/cf_firewall.sh` 不删：
> 将来若迁回云服务器可直接复用。

---

## 4. 系统安装

### 4.1 装系统

1. 用 U 盘装 **Ubuntu Server 24.04 LTS**（不要桌面版）
2. 分区：整个 512 GB 给 `/`，文件系统 ext4；swap 设 8 GB
3. 外接 1 TB 盘装完系统后再格式化，挂 `/backup`

```bash
# 确认外接盘设备名（不要选错，会格式化）
lsblk
sudo mkfs.ext4 /dev/sdX1
sudo mkdir -p /backup
# 写入 /etc/fstab（用 UUID，别用 /dev/sdX，顺序会变）
sudo blkid /dev/sdX1
sudo tee -a /etc/fstab <<'EOF'
UUID=<上面的 UUID>  /backup  ext4  defaults,nofail  0  2
EOF
sudo mount -a
```

> `nofail` 很重要：外接盘没插时系统仍能正常启动，不会卡在救援模式。

### 4.2 基础配置

```bash
# 以 root 登录后
adduser deploy && usermod -aG sudo deploy
# 把本机公钥放进 /home/deploy/.ssh/authorized_keys，测试 ssh deploy@<IP>
# 确认密钥登录可用后，再关闭密码登录：
sudo sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
sudo systemctl restart ssh

timedatectl set-timezone Asia/Shanghai
apt update && apt upgrade -y
```

### 4.3 防火墙

网站本身只有 SSH 需要入站：

```bash
sudo ufw allow from <你的固定 IP> to any port 22 proto tcp
sudo ufw default deny incoming
sudo ufw default allow outgoing
```

如果按第 4.5 节自建 RustDesk，再加这四个（CGNAT 时不要开，见 4.5.3）：

```bash
sudo ufw allow 21115:21117/tcp
sudo ufw allow 21116/udp
```

80/443 **不需要开放**——Tunnel 是出站连接。

网页走 Tunnel、RustDesk 走端口转发，两套入口互不干扰：
前者是**出站**长连接，后者是**入站** TCP/UDP，不抢端口。

```bash
sudo ufw enable && sudo ufw status numbered
```

### 4.4 防休眠

迷你主机不需要合盖设置，但要确认：

```bash
# 系统不能自动挂起
sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
```

### 4.5 RustDesk 服务端（可选）

自建 hbbs（ID 注册）+ hbbr（中继转发），让 iPad 走蜂窝时不再绕美西中继
（现在是 600 ms，家里中继通常能降到 100 ms 以内）。资源占用可以忽略：

| 服务 | 内存 | 磁盘 |
| --- | --- | --- |
| hbbs | 20–40 MB | 密钥 < 1 KB |
| hbbr | 空载 20–40 MB；中继几路时 50–150 MB | 日志几 MB |

hbbr 只转发端到端加密流量，不转码，CPU 基本不占。
真正吃紧的是**上行带宽**：一路中继约占 10–20 Mbps，和素材上传、
restic 同步 OSS 抢同一条线。同一局域网内直连不消耗这个。

**前提**：家宽要有**非 CGNAT 的公网 IPv4**（路由器 WAN 口 IP 与
`curl ip.sb` 的结果一致）。拍板的顺序是先查这个，再决定要不要装。

#### 4.5.1 起容器

单独放 `/srv/rustdesk`，**不要**并进 `infra/docker-compose.prod.yml`：
它用 `network_mode: host`，和 Caddy 的 bridge 网络是两套东西。

```bash
sudo mkdir -p /srv/rustdesk/data && sudo chown -R deploy:deploy /srv/rustdesk
cd /srv/rustdesk
openssl rand -hex 32          # 生成 key，记进密码管理器
```

`/srv/rustdesk/docker-compose.yml`：

```yaml
services:
  hbbs:
    image: rustdesk/rustdesk-server:latest   # 上线后锁成具体版本
    command: hbbs -r <域名或公网 IP>:21117 -k <key>
    network_mode: host
    volumes:
      - ./data:/root
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"

  hbbr:
    image: rustdesk/rustdesk-server:latest
    command: hbbr -k <key>
    network_mode: host
    volumes:
      - ./data:/root
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

```bash
docker compose up -d && docker compose ps    # hbbs / hbbr 都要 Up
```

`./data` 里会生成 `id_ed25519` / `id_ed25519.pub`，**这两个文件必须备份**
（第 9 节已纳入 restic）。换了 key 所有客户端都要重配。

**21114 不要开**：那是 Web 控制台与地址簿云同步，纯远程控制用不到。

#### 4.5.2 端口转发与 DDNS

| 端口 | 协议 | 用途 |
| --- | --- | --- |
| 21115 | tcp | NAT 类型测试 |
| 21116 | tcp + **udp** | ID 注册与心跳 |
| 21117 | tcp | 中继转发 |

四个都要，少一个就是连不上、或者只能被动走中继。路由器里转发到迷你主机
内网 IP，ufw 按 4.3 节放行。公网 IP 是动态的，配一个 DDNS
（阿里云 / Cloudflare / DuckDNS 都行），客户端一律填域名、不填 IP。

> 运营商通常只封 80/443 入站，21115–21117 一般不受限——
> 所以 RustDesk 比在家架网站更容易过。

#### 4.5.3 没有公网 IPv4 时

CGNAT、或运营商根本不给公网地址时，两条退路：

1. **用 cloudflared 转发 TCP**：Tunnel 里加两条 ingress，
   `service: tcp://hbbs:21116` 与 `tcp://hbbr:21117`。
   代价：延迟 +50–200 ms；**UDP 用不上**，hbbs 的 NAT 类型检测与打洞退化，
   nat_type 大概率停在 2/3，更多连接落到 hbbr 中继，
   中继流量吃的是家宽上行。
2. **不自建**，继续用公共服务器。

#### 4.5.4 客户端三处都要改

| 位置 | 改什么 |
| --- | --- |
| `%APPDATA%\RustDesk\config\RustDesk2.toml` | ID 服务器、relay、key |
| 服务侧配置（LocalService 目录，需管理员） | 同上；必须**停服务 → 改文件 → 起服务**，否则退出时用内存里的旧配置覆盖回去 |
| iPad RustDesk → 设置 → ID/中继服务器 | 同样的地址与 key |

改完要**重启 RustDesk 托盘进程**——它只在启动时读配置，不重启等于没改。

本机 Clash 还要把新服务器排除掉，否则流量钻进 TUN、nat_type 变回 2：
`tun.route-exclude-address` 加真实 IP；用域名时同时把域名加进
`dns.fake-ip-filter`，否则解析出 198.18.x 假地址，上面那条规则匹配不上。

#### 4.5.5 验收

```bash
nc -zvu <域名> 21116        # UDP
nc -zv  <域名> 21117        # TCP
```

- iPad 连上后延迟 **< 100 ms**：说明中继就在家门口
- nat_type 从 2 变成 **1**：连 P2P 打洞都通了，最好结果
- 断开重连两次，再重启一次迷你主机，确认两个容器自己起来

#### 4.5.6 一个绕不开的循环依赖

RustDesk 服务端跑在你要管理的那台机器上：机器假死或断网，远程入口同时消失。
兜底只有智能插座断电重启 + BIOS「来电自启」（见 11.1）；
客户端里保留公共服务器作备用能救急。

> 另有一份可勾选的部署清单（不在仓库里）：
> `C:\Users\willi\Documents\RustDeskServer\deploy-checklist.html`

---

## 5. Docker 与镜像加速

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker deploy
# 重新登录后
docker --version && docker compose version
```

大陆直连 Docker Hub 很慢，配镜像加速（阿里云 ACR → 镜像工具 → 镜像加速器）：

```bash
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json >/dev/null <<'EOF'
{
  "registry-mirrors": ["https://<你的ID>.mirror.aliyuncs.com"],
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" }
}
EOF
sudo systemctl restart docker
docker info | grep -A2 "Registry Mirrors"
```

> Docker 的 `data-root` **保持默认**（系统盘 `/var/lib/docker`）。
> 没有第二块内置盘，外接 USB 盘不适合放 Docker 数据。

---

## 6. 仓库改造（2026-11-17 已完成）

以下改动已经在仓库里落地，到货后直接部署。

### 6.1 `infra/docker-compose.prod.yml`

- **没有 Python 的 `api` 服务**：对外接口全部由 `go-api` 提供
- **`migrate` 服务**：与 `go-api` 共用同一个镜像，`command: ["migrate"]` 跑完即退出；
  `go-api` 用 `depends_on: service_completed_successfully` 等它成功——忘跑迁移时
  服务根本不会起来，而不是带着旧 schema 报 500
- **`caddy` 不暴露端口、不申请证书**：只监听 `:80`，在 compose 网络内被 cloudflared 访问
- **`tunnel` 服务**：`cloudflare/cloudflared`，Token 走环境变量 `TUNNEL_TOKEN`
  而不是命令行参数——argv 会出现在 `docker inspect` 与进程列表里
- `APP_BEHIND_CLOUDFLARE` 固定为 `"true"`。这个前提成立是因为源站没有任何入站端口，
  只能经隧道到达

### 6.2 `infra/Caddyfile`

站点地址 `:80`，删掉证书片段、TLS 证书挂载与 `@goapi` 灰度分流（纯 Go 之后所有路径
都指向 `go-api`）。

### 6.3 `infra/.env.prod.example`

删掉 `TLS_MODE`、`ACME_EMAIL`、`CADDY_HTTP_PORT`、`CADDY_HTTPS_PORT`；新增
`CLOUDFLARE_TUNNEL_TOKEN`、`GO_API_IMAGE`、`GOPROXY`，以及赛季收口与素材配额的可选项。

### 6.4 迁移与建号（纯 Go）

Python 退场后，建表不再依赖 Alembic 运行时：

| 目标 | 命令 |
| --- | --- |
| 建表 / 升级 | `docker compose ... up -d`（自动先跑 `migrate`） |
| 手动补跑 | `docker compose ... run --rm migrate`（幂等） |
| 建管理员 | `docker compose ... run --rm migrate create-admin --email ... --password-stdin` |

迁移 SQL 内嵌在 Go 二进制里（`backend-go/internal/migrate/sql/`），按文件名顺序执行，
每个文件一个事务，`schema_migrations` 记账，多实例并发时用 advisory lock 串行。
建表规则见 `backend-go/README.md`「schema 变更」。

### 6.5 备份

`scripts/backup.sh`：`pg_dump -Fc` 流式进 restic、素材卷（按容器挂载点定位，不依赖
compose 项目名）、`.env.prod` 与 compose 文件、RustDesk 密钥；保留 7 天 / 4 周 / 6 个月；
设置 `OSSUTIL_BUCKET` 后把 restic 仓库整体同步到对象存储。

---

## 7. 首次部署

前提：系统装好、Docker 与 Compose v2 就绪（第 4、5 节），Cloudflare Tunnel 可以稍后建
（没有 Token 时 `tunnel` 容器会重启，不影响建库）。

```bash
sudo mkdir -p /srv && sudo chown "$USER":"$USER" /srv
git clone <仓库地址> /srv/app && cd /srv/app

cp infra/.env.prod.example infra/.env.prod
chmod 600 infra/.env.prod
$EDITOR infra/.env.prod    # 填 DOMAIN / POSTGRES_PASSWORD / APP_BOT_CHECK_SECRET / CLOUDFLARE_TUNNEL_TOKEN

COMPOSE="docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod"

# 构建并启动。migrate 先建表，go-api 等它成功后才起
$COMPOSE up -d --build

# 期望：db(healthy) / migrate(Exited 0) / go-api(healthy) / caddy / tunnel
$COMPOSE ps -a

# 建第一个管理员。口令走标准输入，不留在 shell 历史与进程列表里
printf '强口令
' | $COMPOSE run --rm -T migrate create-admin     --email you@example.com --name 你的名字 --password-stdin

# 容器内自检（8000 端口没有对外暴露）
$COMPOSE exec -T go-api wget -qO- http://127.0.0.1:8000/api/v1/health
```

隧道建好、DNS 指过来之后：

```bash
curl -s https://<DOMAIN>/api/v1/health
```

> **构建在哪跑**：16 GB 的迷你主机直接 `--build` 没问题（Go 编译约 2 GB 内存）。
> 那句「不要在服务器上 docker build」是针对 2C3.5G 的旧 VPS，不适用于这台机器。

## 8. Cloudflare Tunnel

### 8.1 建 Tunnel

1. Cloudflare 控制台 → **Zero Trust → Networks → Tunnels** → Create a tunnel
2. 选 **Cloudflared** → 起名 `challenge-home`
3. 复制 Token（形如 `eyJ...`），填进 `infra/.env.prod` 的
   `CLOUDFLARE_TUNNEL_TOKEN`
4. 在 Tunnel 的 **Public Hostname** 里加：

   | 字段 | 值 |
   | --- | --- |
   | Subdomain | 你的子域 |
   | Domain | 你的域名 |
   | Service | `http://caddy:80` |

5. 保存后 DNS 自动生成一条 CNAME（橙云），不需要手动建 A 记录

### 8.2 验证

```bash
docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod logs -f tunnel
# 应看到 "Registered tunnel connection"

curl -s https://你的域名/api/v1/health
# {"status":"ok","version":"...","database":"ok"}
```

### 8.3 控制台设置

| 位置 | 设置 | 值 |
| --- | --- | --- |
| SSL/TLS → Overview | 加密模式 | Full (strict) |
| SSL/TLS → Edge Certificates | Always Use HTTPS | 开 |
| SSL/TLS → Edge Certificates | Minimum TLS Version | 1.2 |
| Speed → Optimization | Brotli | 开 |
| Network | HTTP/3 | 可以关（SSE 排障更简单） |
| Security → Bots | Bot Fight Mode | **关** |

### 8.4 缓存规则（重要）

家宽上行只有 30–50 Mbps，静态资源必须走 Cloudflare 边缘：

| 路径 | 缓存 |
| --- | --- |
| `/uploads/*` | Cache Everything，Edge TTL 1 天 |
| `*.js` / `*.css` / 字体 / 图片 | 默认缓存 |
| `/api/*` | **不缓存** |

---

## 9. 备份（restic）

### 9.1 为什么不用每日全量 tar

`tar czf` 每次从头打包整个目录，保留 14 天就是 14 份完整副本。
restic 做**内容去重**，25 GB 数据保留 7 天 + 4 周 + 6 个月只需约 40 GB。

另外：素材（JPEG/PNG/MP4）本身已压缩，`tar czf` 的 gzip 收益 < 5%，白耗 CPU。

### 9.2 初始化

```bash
sudo mkdir -p /backup/restic /etc/restic
sudo chmod 700 /backup/restic
sudo apt install -y restic

# 备份仓库密码——丢了就永远打不开
openssl rand -base64 32 | sudo tee /etc/restic/password >/dev/null
sudo chmod 600 /etc/restic/password

export RESTIC_REPOSITORY=/backup/restic
export RESTIC_PASSWORD_FILE=/etc/restic/password
sudo -E restic init
```

> **仓库密码要单独存一份**（密码管理器或打印出来放抽屉）。
> 它不参与任何自动化流程，忘了备份就是废数据。

### 9.3 每日备份脚本

仓库里已经写好 `scripts/backup.sh`，直接装进 cron 即可：

```cron
30 3 * * * /srv/app/scripts/backup.sh >> /var/log/backup.log 2>&1
```

它做四件事，每件都是流式写进 restic，宿主机上不落临时文件：

| 内容 | 说明 |
| --- | --- |
| 数据库 | `pg_dump -Fc` 通过管道进 `restic backup --stdin` |
| 素材 | 按 go-api 容器的 `/data/uploads` 挂载点定位卷的真实路径，不猜 compose 项目名 |
| 配置 | `.env.prod`、compose 文件、Caddyfile；没有 `.env.prod` 就等于丢了数据库口令 |
| RustDesk | `/srv/rustdesk/data` 存在时才备份（丢了要重建整个远程访问网络） |

保留策略 `--keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune`；
设置 `OSSUTIL_BUCKET=oss://<bucket>/restic/` 后额外把 restic 仓库同步一份到对象存储。
脚本用 `flock` 防止重叠运行，路径都可以用环境变量覆盖（见脚本头部）。

### 9.4 恢复

```bash
export RESTIC_REPOSITORY=/backup/restic
export RESTIC_PASSWORD_FILE=/etc/restic/password

restic snapshots                        # 看有哪些快照
restic restore <id> --target /tmp/restore

# 数据库（custom 格式，用 pg_restore）
restic dump <id> challenge.dump | docker compose   -f /srv/app/infra/docker-compose.prod.yml --env-file /srv/app/infra/.env.prod   exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists'
```

### 9.5 每月验证

备份没验证过等于没有。每月做一次：

```bash
restic check --read-data-subset=5%
restic restore latest --target /tmp/verify && ls -R /tmp/verify | head
```

---

## 10. 直播与回放

直播**不经过迷你主机**，主机只负责网站。

### 10.1 推流

```
现场 OBS（4K60）
  ├─ RTMP  ──▶ B 站（1080p60，免费）
  └─ RTMPS ──▶ YouTube（4K60，免费，需频道 ≥ 50 订阅）
```

- 多路推流用 OBS 插件 **obs-multi-rtmp**
- 现场网络上行 ≥ 50 Mbps，优先有线
- 4K60 推 YouTube 约 20–25 Mbps，B 站 1080p 约 6–8 Mbps

### 10.2 网站嵌入

```html
<!-- YouTube 直播 -->
<iframe src="https://www.youtube.com/embed/live_stream?channel=<CHANNEL_ID>"
        allow="autoplay; encrypted-media" allowfullscreen></iframe>

<!-- B 站直播 -->
<iframe src="https://live.bilibili.com/blanc/<房间号>?hideDanmu=1"
        allowfullscreen></iframe>
```

**双通道原因**：YouTube 在大陆无法直连，B 站大陆可访问但 4K 直播需要权限。
回放页给两个入口按钮，不做自动检测。

### 10.3 回放

- 视频上传到 **YouTube（4K）** 与 **B 站（1080p）**
- 网站只存 `youtube_id` / `bvid` 与元数据，**不存视频文件**
- 播放器 iframe 嵌入

### 10.4 不采用 Cloudflare Stream 的原因

参考成本：几百人观看的直播约 ¥1,700/年，回放分发再 ¥1,000–3,000/年。
B 站 + YouTube 方案费用为 ¥0，代价是延迟 10–30 秒、B 站最高 1080p、
体验分散在两个平台。

若将来需要低延迟互动或统一体验，再切 Cloudflare Stream；网站侧留好
"播放器地址可配置"的接口即可切换。

---

## 11. 日常运维

| 项 | 命令 |
| --- | --- |
| 容器状态 | `docker compose -f infra/docker-compose.prod.yml ps` |
| 日志 | `docker compose logs -f --tail 200 go-api` |
| 磁盘 | `df -h /` 和 `df -h /backup` |
| Docker 占用 | `docker system df` |
| 时间同步 | `timedatectl status` |
| restic 快照 | `restic snapshots` |
| 备份日志 | `tail -50 /var/log/backup.log` |

### 11.1 开机自启

BIOS 里打开 **AC Power Loss → Power On**（断电恢复后自动开机）。
迷你主机没有 IPMI，机器假死时用可远程控制的智能插座断电重启。

### 11.2 升级

```bash
cd /srv/app && git pull
COMPOSE="docker compose -f infra/docker-compose.prod.yml --env-file infra/.env.prod"
# 有 schema 变更时 migrate 会先跑；失败了 go-api 不会启动，看 logs migrate
$COMPOSE up -d --build
$COMPOSE ps -a
```

回滚：`git checkout <上一个版本> && $COMPOSE up -d --build`。
数据库迁移只向前，回滚代码不会回滚表结构——新增列/表都向后兼容，删改类变更要单独写
反向迁移并确认。

---

## 12. 风险与未决

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| **无 ICP 备案** | 对外提供服务不合规；主体是学校时更敏感 | 明确用途边界（校内内测）；正式对外改回学校机房/云服务器 |
| 家宽上行有限 | 几百人访问网站时静态资源慢 | 配 Cache Rules；监控带宽 |
| 断电 | 服务中断 | UPS + 来电自启 |
| 断网 | 服务中断 | 4G/5G 热点兜底；活动前检查 |
| 单机故障 | 全站不可用 | restic + OSS 异地；备一台旧机器 |
| 平台依赖 | B 站/YouTube 限流、封禁 | 双平台 + 保留 CF Stream 切换能力 |
| 16 GB 内存 | 并发高峰可能吃紧 | 实测 < 1.2 GB；可后期加 SODIMM |
| RustDesk 与主机同生共死 | 机器假死或断网时，远程入口一起消失 | 智能插座 + BIOS 来电自启；客户端保留公共服务器备用 |

### 待办

- [ ] 硬件采购（迷你主机 16/512 + 1 TB 移动 SSD + UPS）
- [ ] 装系统、分区、挂 `/backup`
- [ ] 按第 6 节改造仓库
- [ ] 建 Tunnel、填 Token
- [ ] 首次部署与全链路验证
- [ ] 创建 `scripts/backup.sh` 并入 cron
- [ ] 配置 Cache Rules
- [ ] （可选）RustDesk 自建：查公网 IPv4 → 端口转发 / DDNS → 客户端三处配置
- [ ] （可选）`/srv/rustdesk/data` 纳入 restic
- [ ] OSS bucket 与 `ossutil`
- [ ] 直播页 / 回放页的前后端功能（独立排期）
