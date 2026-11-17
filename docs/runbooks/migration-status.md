# 迁移状态总览

> 状态快照：**2026-11-15**。
>
> 一句话：**后端迁移已 100% 完成并通过全部上线前验证，但没有上线**；
> 部署目标已改为「自建迷你主机 + Cloudflare Tunnel」；
> **原站（Unscripted 生产）数据已于 2026-11-15 全量取回并验证可恢复**（见第 5 节）。

---

## 1. 迁移进度

| 项 | 状态 | 数字 |
| --- | --- | --- |
| 接口实现 | **完成** | 279 / 279 个 OpenAPI operation |
| 对照测试 | **完成** | 20 个套件、约 477 项断言，与 Python 实现逐字段比对 |
| 会话互通 | **完成** | 跨语言会话、OAuth 令牌、激活链接可互通 |
| 并发正确性 | **完成** | 20 / 20（注册、投票上限、编辑锁、slug 冲突） |
| 权限矩阵 | **完成** | 30 / 30（匿名 / participant / member / captain / staff / admin） |
| 限流 | **完成** | 28 / 28（登录、发码、校验 → 429 + `Retry-After`） |
| 上传安全 | **完成** | 28 / 28（路径穿越、分片排序、总量上限、类型白名单） |
| 安全响应头 / CORS | **完成** | 40 / 40 |
| 定时广播 worker | **完成** | 23 / 23（条件更新认领，多实例不重发） |
| 业务邮件全链路 | **完成** | 36 / 36（验证码、激活、重置、邀请、广播） |
| 备份恢复演练 | **完成** | 数据库 8 / 8、uploads 卷 4 / 4 |
| 安全扫描 | **完成** | `govulncheck` 影响代码漏洞 0、`pip-audit` 无已知漏洞 |
| 静态检查 | **完成** | `go build` / `go vet` / `go test` 全绿；`pytest` 97 项；`ruff` 无告警 |
| 空库冷启动 | **完成** | 44 张表、管理员与赛事脚本、注册登录全流程 |
| 真实邮件 | **完成** | Gmail 应用专用密码，Python 与 Go 各真实发出一封 |
| Caddy 灰度与回滚 | **完成** | 本机 + 真实域名 TLS 环境各演练一次 |
| 后端补强（2026-11-15） | **完成** | LLM 流式转发、图片变体管线、SSE 广播、上传生命周期，见下表 |
| 赛季能力（2026-11-17） | **完成** | 一届一收口：前序作品、空档期身份回落、按届报名、后台建赛，见下表 |

### 2026-11-15 补强

原产品就没做的四处欠账，这次一并补上（都有单元测试，但**还没对着真实上游与真库跑过**）：

| 项 | 原来的样子 | 现在 |
| --- | --- | --- |
| `/v1/chat/completions` 流式 | 501 `stream_unsupported`，Python 侧也没有 | 真流式转发；自动注入 `stream_options.include_usage`；**拿不到用量就不发 `[DONE]`**，改发错误事件并把该上游标记为不可用于流式 |
| 图片变体 | `GET /files/{id}/{variant}` 恒 404 | 上传后转 webp 并生成 w1600/w960/w480；GIF 与视频原样保存 |
| SSE | 每条连接各自轮询 DB | `internal/broker`：一个轮询器广播给全部连接，无人订阅时不查库 |
| 上传生命周期 | 无孤儿清理、无配额 | 过期分片与超时未完成素材定时清理；素材总占用可设上限，超出返回 507 |

### 2026-11-17 赛季能力

网站要持续办下去，所以把「一届比赛」做成了真正的产品概念，见
[`../design/season-lifecycle.md`](../design/season-lifecycle.md)。

| 项 | 原来的样子 | 现在 |
| --- | --- | --- |
| 前序作品 | 没有概念，画廊把所有届混在一起 | `GET /exhibition/archive` 与 `/{event_id}`；赛事结束满 30 天自动写 `archived_at`，作品从进行中的页面移出 |
| 空档期身份 | 参赛者身份永久保留，只能手工改 | 收口时 `member/captain → participant` 自动回落，管理员与工作人员除外 |
| 报名 | 只有全局的「旁观/参赛」切换，不分届 | `event_enrollments` 按届记录；`POST /events/{id}/enrollments`，受 `registration_open` 约束 |
| 建下一届 | 只能 SSH 跑 Python 脚本 | `POST /admin/events` |
| 赛后提交 | 收口后作品被锁成只读 | 赛事级 `post_event_edits`（默认开）：赛后与收口后都能继续提交新版本，前序作品同时给出参赛版本号 |
| 多届正确性 | 5 处成员关系查询没按赛事限定，会串届 | 全部按 `companies.event_id` 限定 |

### 性能（同一份基准数据）

| 接口 | Go | Python |
| --- | --- | --- |
| `/companies` | 3,811 rps，p50 12.5 ms | 303 rps，p50 158 ms |
| `/projects` | 5,084 rps，p50 9.5 ms | 475 rps，p50 101 ms |
| `/exhibition/feed` | 4,797 rps，p50 9.5 ms | 328 rps，p50 146 ms |
| `/votes` | 3,925 rps，p50 12.3 ms | 311 rps，p50 155 ms |

- 全 Go 单独跑：`-c 50`、**5,010 rps**、20,000 请求 0 错误，Postgres 连接峰值 21
- SSE 200 并发首帧：本机 8 核 Go p50 **4 ms**（Python 680 ms）；
  2 vCPU 测试实例 p50 67–99 ms（Python 1.06 s）
- 2 vCPU 测试实例：13.7 万请求 0 错误、内存平稳

> 明细与证据见 [`go-live-checklist.md`](go-live-checklist.md)。

---

## 2. 仓库状态

**最新提交**：`185d4da`（2026-11-04 17:14），之后的工作**全部未提交**。

```
71 个文件有改动：44 个已跟踪文件被修改 + 27 个新增文件
git diff --stat：44 files changed, 1389 insertions(+), 410 deletions(-)
```

未提交的内容分三类：

| 类别 | 内容 |
| --- | --- |
| **第二轮加固**（代码） | 限流（`ratelimit.go` / `ratelimit.py`）、受信代理取址、定时广播 worker、上传加固、CORS 对齐 |
| **第二轮测试** | `tests/parity/` 新增 6 个套件（限流、广播、上传安全、响应头、业务邮件、uploads 卷备份） |
| **部署文档与工具** | `cloudflare-migration.md`、`deploy-selfhost.md`、`scripts/cf_provision.py`、`scripts/cf_firewall.sh`、`infra/caddy/` |

> 这些改动**只在本机工作区**，没有任何异地副本。提交前不要清理。

---

## 3. 部署目标

2026-11-05 决定：**自建迷你主机 + Cloudflare Tunnel**，纯 Go 端上线。
仓库侧的 compose / Caddy / 变量模板 / 内置迁移 / 备份脚本**已于 2026-11-17 备好**，
硬件到货后按 `deploy-selfhost.md` 第 7 节直接部署。

```
访客 → Cloudflare（TLS 终结、WAF、缓存）
     │  cloudflared 出站连接 443
     ▼
迷你主机：cloudflared → Caddy :80 → go-api :8000 → db:5432 + uploads 卷
备份：restic → /backup（外接 1 TB）└→ ossutil → 阿里云 OSS
```

三条路线并存，按**是否需要 ICP 备案**选择：

| 路线 | 文档 | 适用 |
| --- | --- | --- |
| 自建迷你主机 + Tunnel | [`deploy-selfhost.md`](deploy-selfhost.md) | 当前方案，校内内测 |
| 阿里云 ECS + 备案 | [`deploy-cn.md`](deploy-cn.md) | 正式对外 |
| 当前 VPS（与 Unscripted 共用） | [`cloudflare-migration.md`](cloudflare-migration.md) | 仅验证用 |

硬件：迷你主机 16 GB / 512 GB（≈ ¥2,400）+ 1 TB 移动 SSD（¥450）+ UPS（¥600），
运行成本约 ¥275/年。**内存与磁盘对现有负载绰绰有余**：四个容器实测 < 1.2 GB。
再叠加自建 RustDesk（hbbs + hbbr）也只多约 300 MB（见 `deploy-selfhost.md` §4.5）。

---

## 4. 待办

- [x] **取回原站数据**（2026-11-15 完成，见第 5 节）
- [ ] 把备份复制一份到异地（本机不是唯一副本）
- [ ] 硬件采购：迷你主机 16/512 + 1 TB 移动 SSD + UPS
- [ ] 装系统、分区、挂 `/backup`
- [x] 按 `deploy-selfhost.md` 第 6 节改造仓库（2026-11-17）
      ：compose 删 `api` 加 `tunnel` 与一次性 `migrate`；Caddyfile 改 `:80`；env 模板换变量
- [x] 纯 Go 运维入口（2026-11-17）：`./api migrate` 建表、`./api create-admin` 建号，
      机器上不需要 Python
- [x] `scripts/backup.sh`（restic + 可选 ossutil）（2026-11-17）
- [ ] 建 Cloudflare Tunnel、填 Token、配 Cache Rules
- [ ] 首次部署与全链路验证（健康检查 / SSE / 上传 / 邮件 / 真实客户端 IP）
- [ ] 备份脚本装进 cron 并做一次恢复演练
- [ ] OSS bucket 与 `ossutil`
- [ ] 提交工作区（49 个文件）——**用户未要求，等指令**
- [ ] 直播页 / 回放页的前后端功能（独立排期）
- [ ] 决定是否正式对外（决定要不要回到学校机房 / 云服务器 + ICP 备案）
- [ ] **首次部署验证通过后删除 Python 实现**（2026-11-17 决定）。删掉 `backend/`、
      `tests/parity/` 与 `scripts/export_schema_sql.py`；此后 `docs/api/openapi.json`
      与 `backend-go/internal/migrate/sql/` 手工维护。删除前它们仍是行为参照

---

## 5. 原站数据保全（2026-11-15 完成）

### 5.1 数据在哪

原网站 = Unscripted 生产站（`scie1024.dev`），仓库 `github.com/JaaamesZhang/Unscripted`，
部署在单台 VPS `47.236.14.84`（阿里云新加坡，Ubuntu 24.04，2 vCPU / 3.5 GB / 50 GB）。

需要保全的两类数据：

| 数据 | 位置 |
| --- | --- |
| 报名注册的参赛者、公司、投票、奖项等 | Postgres 容器 `anything-inc-postgres`，Docker 卷 |
| 作品文件 / 素材 | `shared/data/uploads/`（宿主机目录） |

### 5.2 已有备份机制

`infra/deploy/server/backup.sh`：

- `pg_dump -Fc` → `backups/db/`，每晚 03:30 跑一次，保留 **14** 份
- 素材：**同盘硬链接**快照 → `backups/media/`，保留 **7** 份
- 每次部署前和回滚前各自动备份一次

**两个必须知道的事实**：

1. 所有备份都写在**这台服务器的盘上**，磁盘挂了一起没。
2. 仓库文档要求「定期拷到服务器外面」，`docs/runbooks/launch-checklist.md` 里那条
   「An off-site copy of the backups exists」**没有打勾**。

### 5.3 当前状态（2026-11-15 实测）

**源站不可达。**

| 检查 | 结果 |
| --- | --- |
| `https://scie1024.dev/` 经 Cloudflare | **522**（回源超时），19.5 s |
| 同上，用外部服务 `allorigins` 复测 | **522** |
| 域名解析 | 正常（CF 边缘 `104.21.53.6` / `172.67.206.202`） |
| HTTP 入口 | 301 正常（CF 在响应） |
| 源站 SSH 22 | `Connection closed`（多路径、带退避重试均失败） |
| 本机是否有数据副本 | **没有**（全盘搜 `*.dump` / `*.sql.gz` / `backups/` 无生产数据） |

结论：**Cloudflare 能到边缘但到不了源站。**

2026-11-15 用户确认：这台服务器是**主动关闭**的，不是故障，522 属预期。

### 5.4 取数结果（2026-11-15 完成）

服务器 10:51 重新开机，值守脚本 90 秒内连上，全部数据已取回本机：

| 内容 | 数量 | 体积 |
| --- | --- | --- |
| 整库逻辑备份 | 1 份 | 4.4 MB |
| 服务器原有备份 | 24 份（14 nightly + 10 pre-deploy，10-24 ~ 11-12） | 103 MB |
| 历史素材快照 | 7 个 | 36.5 MB |
| 素材全量 | 101 个文件 | 101 MB |
| Intern 安装包 | 3 个 + `latest.json` / `latest.yml` | 492 MB |
| 表级 CSV | 12 张 | 657 行 |
| 配置 / 密钥 / 状态快照 | — | 150 KB |
| 运维目录（bin / state / logs / caddy） | — | 145 KB |
| **合计** | | **840 MB** |

存放位置：`D:\backups\unscripted\pull-2026-11-15\`，
含 `MANIFEST.txt` 与两份 sha256 清单。

**验证全部通过**：

- 25 份 dump 的 `PGDMP` 头 **25/25 有效**，0 损坏
- 素材 **101 个文件 / 105,989,440 字节**，与服务器逐字节一致
- 安装包 5 个文件 sha256 **全部与服务器一致**
- 导入本机临时 `postgres:17-alpine` 容器，`pg_restore` 无报错
- 恢复后行数与生产完全一致：users 40、registrations 31、companies 14、
  company_members 19、projects 7、project_versions 13、votes 20、
  vote_events 26、media_assets 69

**没有取的三项**（以及为什么）：

| 内容 | 体积 | 原因 |
| --- | --- | --- |
| `shared/data/postgres` | 120 MB | 运行中直接复制是撕裂备份，逻辑 dump 才是正确形式 |
| `releases/` | 713 MB | 构建产物，由源码重建 |
| `repo.git/` | 74 MB | 代码仓库，GitHub 上有同一份 |

### 5.5 取数过程中的三个发现

1. **`du` 报 4.0K 是权限假象**：`shared/data/uploads` 属主是 uid 1000、权限 700，
   `deploy` 用户读不到；统计和打包必须在容器内以 root 做。

2. **单连接被限速 30 KB/s**：8 路并行后达到 **1,207 KB/s（40 倍）**，
   101 MB 素材从约 1 小时压到 100 秒。
   脚本：`D:\backups\unscripted\pull-parallel.py`。

3. **按字节分片下载会踩"边界多读一块"**：`dd bs=1M skip=K count=N` 如果每片
   都多给一块，相邻分片会在边界处重叠，拼出来的文件比原文件大整数个 MiB
   （这次三个安装包各正好多 1 MiB）。校验不能只看"下完了"，必须比对
   sha256 或字节数。这次的重复位置可推算，本地切除后哈希立刻对上，
   没有重下 492 MB。脚本：`D:\backups\unscripted\pull-downloads.py`。

### 5.6 备份文件与脚本位置

备份目录**不进 git**，含用户个人信息与生产密钥：

```
D:\backups\unscripted\pull-2026-11-15\
├── MANIFEST.txt          清单、恢复命令、验证结论
├── SHA256SUMS.txt        84 个归档文件的校验和
├── SHA256_FILES.txt      展开后逐个文件的校验和
├── db/                   采集时刻整库 dump（gzip）
├── uploads/              素材 8 个分片 + extracted/ 解包副本
├── server-backups/       24 份历史 dump + 7 个素材快照
├── full/                 配置、密钥、12 张 CSV、服务器状态
├── server-misc/          Intern 安装包 + bin/state/logs/caddy
└── survey/               环境快照与行数
```

取数脚本在 `D:\backups\unscripted\`（同样不在仓库里）：

| 脚本 | 用途 |
| --- | --- |
| `pull-unscripted.sh` | 值守探测 + 顺序取数（服务器上线自动开始） |
| `pull-parallel.py` | 多连接并行取素材与历史备份，**推荐的正式方式** |
| `pull-downloads.py` | 按字节分片取大文件，自带 sha256 校验 |

重跑：`python D:\backups\unscripted\pull-parallel.py`（目标目录见脚本内 `DEST`）。

### 5.7 后续

- [ ] 把 `D:\backups\unscripted\pull-2026-11-15\` 复制一份到异地
      （移动硬盘 / 对象存储），本机不是唯一副本
- [ ] 该目录含用户个人信息与生产密钥，按敏感数据保管，不要进 git
- [ ] 新平台导入时以 `db/anything-20261115T025002Z.dump.gz` 为基线
- [ ] 新平台上线后按迁移脚本导入，不再依赖原服务器
- [ ] `deploy-selfhost.md` 的 restic + OSS 方案正是为了不再出现
      「备份和源数据同盘」

---

## 6. 时间线

| 日期 | 事件 |
| --- | --- |
| 2026-10-15 ~ 10-23 | Unscripted 活动进行（报名、创作、展览投票、路演） |
| 2026-10-28 | Unscripted 生产到 v0.5.8（直播 / DCDN 相关） |
| 2026-10-30 | 本仓库建立，接口契约先行 |
| 2026-11-04 | Go 后端 279/279 完成；P0 四项验证通过；VPS 隔离测试栈 |
| 2026-11-05 | 部署目标改为自建迷你主机 + Cloudflare Tunnel |
| 2026-11-06 | `deploy-selfhost.md` 完成，补 RustDesk 自建章节 |
| 2026-11-15 | 原站关机导致 522；购买方开机后，当天完成全量取数（840 MB，含 Intern 安装包）并验证可恢复 |
| 2026-11-15 | 后端补强四项（LLM 流式 / 图片变体 / SSE 广播 / 上传生命周期）完成；Intern 桌面客户端在独立仓库 `D:\code\intern` 立项，设计文档见 [`../design/intern-redesign.md`](../design/intern-redesign.md) |
| 2026-11-17 | 赛季能力完成：前序作品、空档期身份回落、按届报名、后台建赛；多届数据正确性问题同步修复 |
