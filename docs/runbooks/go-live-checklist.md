# Go 迁移上线前检查清单

> 状态快照：2026-11-04（进度总览与阻塞项见 [`migration-status.md`](migration-status.md)，2026-11-15）。
> Go 后端已实现全部 279 个 OpenAPI operation，完成本机全量回归，并在一台隔离的
> 测试实例上做过冒烟与持续压测。**公网入口尚未开放，尚未正式上线。**
>
> P0-1（空库冷启动）与 P0-2（素材上传）已于 2026-11-04 在本机验证通过；
> P0-3（真实邮件）驱动完成、已在 VPS 上真实发信，业务全链路也走通；
> P0-4 分流与回滚先在本机演练，随后在真实域名 + TLS 环境下复测通过。
> **P0 全部完成**；尚未正式上线，等发版指令。
>
> **部署目标变更（2026-11-04 决定）**：后续会迁移到 owner 自己的 Cloudflare 账号
> 与自有服务器。当前 VPS 与 Unscripted 共用 Caddy 的演练方式只用于验证；与
> Cloudflare 强相关的项（WAF 拦非浏览器 UA、真实客户端 IP、Origin CA 证书）
> 迁移后按新账号重新复测，不阻塞本轮开发。
> 迁移步骤见 `docs/runbooks/cloudflare-migration.md`；仓库侧工具链已就绪，
> 等域名与 API Token。

---

## 已完成

- **279 / 279 operation**：路由表以 `docs/api/openapi.json` 为准，`go build` / `go vet` /
  `go test` 全部通过。
- **对照测试**：20 个套件、约 477 项断言，同一数据库下与 Python 实现逐字段比对；
  跨语言会话、OAuth 令牌、激活链接均可互通。
- **性能（本机 8 核）**：公开读路径快 8–67 倍（`/companies` 77 → 5,191 rps），
  鉴权路径快 9–19 倍；SSE 200 并发首帧 p50 4 ms（Python 680 ms）。
- **测试实例（2 vCPU 共享机）**：8 个公开接口逐字段一致；冒烟 11/11；
  13.7 万请求 0 错误、内存平稳；公开读路径仍快 4.5–38 倍。
- **顺手修复**：`httpx` 补入 Python 运行时依赖（否则生产镜像无法启动）；
  入队后 `participant` 提升为 `member`；Caddyfile 首尾多余花括号。
- **P0-1 / P0-2 已验证**：空库冷启动（44 张表、管理员与赛事脚本、注册登录全流程）、
  容器内上传全链路（含旧卷权限修复），详见下文。
- **限流与真实客户端 IP**（2026-11-04）：两端同一套滑动窗口限流，登录失败 / 发码 /
  校验超限返回 429 + `Retry-After`；`APP_TRUSTED_PROXIES` 下采信转发头，
  Caddy 后面能按真实访客 IP 计数。详见 7.1。
- **定时广播 worker**（2026-11-04）：两端补齐到点发送，靠条件更新认领，
  多实例同时轮询也不会重复发。详见 11.1。
- **真实域名 + TLS**（2026-11-04）：在 `scie1024.dev` 上通过临时路径前缀完成复测，
  证书、响应头、Go/Python 分流、SSE 流式、回滚全部通过，共享 Caddy 已还原。详见 4.1。

---

## P0：不做完不能上线

### 1. 空库冷启动（2026-11-04 通过）

在 `ubench-pg` 里新建空库 `challenge_cold` 跑完整流程：

- [x] `alembic upgrade head` 在空库上成功（44 张表），重复执行是 no-op
- [x] `scripts/create_admin.py --password-stdin` 建出第一个管理员
- [x] `scripts/create_event.py` 建赛事记录（本轮新增；可重复执行，只覆盖显式给出的字段）
- [x] 注册 → 邮箱验证（从 ConsoleMailer 日志取码）→ 登录；`GET /api/v1/events` 正常
- [x] 空数据下 companies / projects / votes / exhibition feed / offline-sessions / pitch
      与后台看板、用户列表均 200

结果：Python 侧 17/17 断言通过；Go 侧 19/19（7 个公开读接口与 Python 逐字段一致，
`/votes` 仅 `updated_at` 因取请求时刻而不同）。

尚未覆盖：空库下的**写入**流程（建公司、发作品）只在基准数据快照上测过。

### 2. 素材上传全链路（2026-11-04 通过）

原因确认：Go 镜像以 `uid 10001` 运行，命名卷首次挂载时继承镜像里目录的属主；
旧镜像没有预建 `/data/uploads`，Docker 建成 `root:root`，写入 permission denied
（Python 容器以 root 运行，不受影响）。

- [x] `backend-go/Dockerfile` 预建 `/data/uploads` 并 `chown app:app`（新卷直接可用）
- [x] `backend/Dockerfile` 同样预建并 `chown 10001`：消除「api 先建卷 → 卷是
      root:root → Go 写不进」的启动顺序竞态（两种顺序都已实测）
- [x] Go 镜像默认 `APP_UPLOAD_DIR=/data/uploads`，避免未配置时写 `/` 报 500
- [x] 复现旧卷（`root:root`、非空）写入失败，并用一次性 `chown` 修复
- [x] 容器内实测：申请 → 直传 → 读字节 → 替代文本 → 分片 → 合并 → 删除 → 404
- [x] 超大文件返回 413（`too_large`）
- [x] 卷内文件属主为 `10001:10001`

**上线时对已有卷的一次性操作**（`challenge_uploads` 目前是 `root:root`）：

```sh
docker run --rm -v challenge_uploads:/data/uploads --user root \
  --entrypoint sh <go-api 镜像> -c "chown -R app:app /data/uploads"
```

新部署（卷为空）不需要这一步。另注意：容器必须显式设置
`APP_UPLOAD_DIR=/data/uploads`（compose 已设置）；未设置时会退回工作目录下的
`uploads/`，非 root 进程写 `/` 会 500。

### 3. 真实邮件通道（驱动与真实发信已验证）

两侧都已实现 SMTP 驱动，`APP_MAIL_DRIVER=smtp` 时启用：

- [x] Python `SMTPMailer`（ssl / starttls / none），配置走 `APP_SMTP_*`
- [x] Go `SMTPMailer`（465 隐式 TLS 需手写，`net/smtp` 自带只支持 STARTTLS）
- [x] 失败路径：验证码类请求回 500；广播逐条记 `failed` 并落错误原因
      （修掉 Go 侧原先无论成败都写 `sent` 的缺陷）
- [x] 本地假 SMTP 验证：Python 驱动 7/7、Go 端到端 7/7、失败路径 3/3、
      广播失败落库 5/5
- [x] 真实凭据验证：Gmail 应用专用密码，Python 与 Go 各成功发出一封
      （在 VPS 上执行；本机到 Gmail 的 465/587 被网络拦截）
- [x] 广播失败逐条记 `failed`，SMTP 恢复后重发成功（`tests/parity/mail_resend.py`
      14/14，两端）
- [x] 注册验证码、激活、口令重置、邀请、广播逐条走 SMTP 协议全链路：
      假 SMTP 收信 → 从邮件正文取码/取激活令牌 → 完成业务（`tests/parity/mail_flows.py`
      36/36，两端）
- [ ] 上线后在服务器上把以上四条用 Gmail 真实投递再点一遍
      （本机到 Gmail 的 465/587 被网络拦截，只能在服务器复测）

注意：

- Gmail 填「应用专用密码」（需先开两步验证），QQ/163 填授权码；都不能用登录密码。
- **本机无法直连 Gmail SMTP**（465/587 都被拦，代理节点同样拦），本机只能用假 SMTP；
  真实发信要到服务器上验证。QQ 邮箱 465 本机可达，可作备用通道。
- 部署自检：`go run ./tools/mailprobe -to you@example.com`，或服务器上跑
  `./mailprobe -to you@example.com -password-stdin`（口令走 stdin，不进 argv）。
- 免费邮箱都有每日收件人配额（数百封/天），适合验证码与邀请；广播给全场用户
  建议换 Resend / 阿里云邮件推送。

### 4. 反向代理与灰度切流演练（2026-11-04 本机通过）

演练方式：用与 `infra/Caddyfile` 相同的规则（只把站点地址改成 `:80`，并加
`X-Upstream` 响应头用来判断落在哪个后端），Caddy + 两个后端容器 + 空库。

- [x] compose 为 `api` / `go-api` 加了 healthcheck，caddy 等两者健康后再启动
- [x] `caddy validate` 对仓库里的 `infra/Caddyfile` 通过（`DOMAIN=example.com`）
- [x] `events`、`events/*`、`votes` 走 Go（`X-Upstream: go`）
- [x] `projects`、`companies`、`/.well-known/*`、`/v1/*` 走 Python
- [x] SSE `/api/v1/votes/stream` 经 Caddy 首帧 4 ms，未被缓冲
      （Caddy 会 gzip SSE，但逐帧 flush，客户端可正常解压）
- [x] 回滚：换回不含 `@goapi` 的配置并 `caddy reload`，全部流量立即回到 Python
      （SSE 首帧 37 ms）
- [x] 再切回灰度配置，`events` / `votes` 重新命中 Go
- [x] 真实域名 + TLS 证书环境复测（2026-11-04，在 `scie1024.dev` 的
      `/__challenge/*` 前缀上完成，测完已还原共享 Caddy，详见 4.1）
- [ ] 若挂 Cloudflare：验证 `APP_BEHIND_CLOUDFLARE` / `APP_TRUSTED_PROXIES`
      下取到真实客户端 IP（需要先部署带本轮改动的新镜像）

### 4.1 真实域名 + TLS 复测（2026-11-04）

环境：VPS `47.236.14.84` 上 Unscripted 的生产 Caddy（TLS 用 Cloudflare Origin CA
证书，站点走 Cloudflare 橙云）。为不影响原站，只在原域名下加了一段
`handle_path /__challenge/*` 临时路由，测完还原。

- [x] 公网证书：Cloudflare 边缘证书（Google Trust Services WE1），SAN 含
      `scie1024.dev`，有效期到 2027-01-20
- [x] 响应头透传：`strict-transport-security`、`x-content-type-options`、
      `referrer-policy`、`x-frame-options`、`via: 1.1 Caddy`
- [x] 分流正确：`/__challenge/api/v1/events`、`/votes/stream` 命中 Go；
      `/companies`、`/projects` 命中 Python（用两个容器的访问日志核对）
- [x] SSE 经 Cloudflare + Caddy：首帧 264–313 ms，6 秒内 6–7 帧；
      gzip 与 identity 都能正常流式输出，未被缓冲
- [x] 回滚：还原 Caddyfile（sha256 与备份一致）→ reload → 原站与 `/api/v1/health`
      仍 200，`/__challenge/*` 回到 404；断开 Caddy 与 challenge 网络的连接
- [x] 演练后 challenge 栈 `compose down`（`challenge_pgdata`、`challenge_uploads` 保留）

操作要点（下次照做）：

```sh
# 1) 起 challenge 的三个服务（不要起它自己的 caddy，避免抢 80/443）
docker compose -p challenge --env-file infra/.env.prod -f infra/docker-compose.prod.yml \
  up -d --no-build db api go-api
# 2) 让现有 Caddy 能按容器名访问
docker network connect challenge_default anything-inc-caddy-1
# 3) 在 {$DOMAIN} 站点最前面加 handle_path /__challenge/*（先备份 Caddyfile）
caddy validate / caddy reload（在 anything-inc-caddy-1 里执行）
# 4) 测完：还原 Caddyfile → reload → docker network disconnect → compose down
```

注意：

- 服务器上的 `challenge-api` / `challenge-go-api` 镜像是本轮加固之前的版本，
  这次只验证了 TLS、分流与 SSE；限流、真实客户端 IP、新的 CORS 响应要等新镜像
  部署后再测。
- Cloudflare 会拦非浏览器 UA（`Python-urllib/*` 直接 403），对接脚本要么用
  正常 UA，要么在 Cloudflare 加 WAF 例外——上线前要确认这一点。

---

## P1：影响功能正确性

### 5. LLM 网关与 OAuth（真实环境）

- [ ] 生产的 LLM 上游（new-api）部署到服务器可达的位置
- [ ] `/v1/chat/completions` 真实转发与计费；预算耗尽、三档熔断
- [ ] OAuth 授权码 + PKCE 用真实域名与回调地址走一遍

### 6. 并发与一致性（2026-11-04 通过）

`tests/parity/concurrency.py`：同一数据库下分别打 Go 与 Python，20/20 通过。

- [x] 同一邮箱并发注册：全部 202，无 5xx
      （修复：唯一约束冲突改为回滚后按已存在处理）
- [x] 两人抢同一作品编辑锁：恰好一个 200、一个 409
      （修复：两端都在事务里锁作品行）
- [x] 同一用户并发投票：接受数 ≤ 钱包上限 3
      （修复：投票前锁用户行，Python 与 Go 都改成事务内串行）
- [x] 同名公司并发创建：都 201、slug 自动加后缀且唯一
      （修复：唯一约束冲突后重算 slug 重试）
- [x] 唯一约束冲突返回 409 而不是 500

### 7. 安全专项

- [x] `govulncheck ./...`：影响代码的漏洞 0（1 个模块级漏洞未被调用）
- [x] `pip-audit --path .venv/Lib/site-packages`：无已知漏洞
- [x] 权限矩阵：匿名 401、participant/member/captain 403、staff/admin 200，
      30 项在两端完全一致（`tests/parity/permissions.py`）
- [x] 登录暴力尝试、注册刷量：两端都加了进程内滑动窗口限流（2026-11-04，
      `tests/parity/ratelimit.py` 28/28，两端行为一致），详见第 7.1 节
- [x] 上传滥用：文件名穿越、分片顺序、总量上限、下发类型
      （2026-11-04，`tests/parity/upload_security.py` 28/28），详见 7.2
- [x] cookie `HttpOnly`/`Secure`/`SameSite=Lax`、CORS 白名单、密钥不入库
      （`tests/parity/security_headers.py` 40/40）；TLS 由 Caddy 终结，待真实域名复测
- [x] 接口级限流：认证接口默认开启；全域每 IP 速率由 `APP_RATE_LIMIT_RPS`
      控制（默认 0 = 关闭，生产 compose 设为 50）

### 7.2 上传与响应头加固（2026-11-04）

- 文件名 `_safe()` 只留 `[A-Za-z0-9._-]`，`../`、反斜杠、控制字符都会被替换；
  存储键是 `<服务端生成的 id>/<清洗后的文件名>`，路径穿越读不到别的文件。
- 分片按**数字序号**合并（之前按字典序，`part10` 会排到 `part2` 前面，超过 9 片就拼错）。
- 单文件上限同时约束单次上传、分片累计和合并结果；超过返回 413 `too_large`；
  单个素材分片数上限 1000，避免海量小文件占满 inode。
- 下发时只放行白名单类型（图片、PDF、ZIP、常见音视频、纯文本、octet-stream），
  其余（如客户端声明的 `text/html`、`image/svg+xml`）一律按
  `application/octet-stream` + `Content-Disposition: attachment` + `nosniff` 返回。
- CORS 方法与预检响应在两端逐字一致：预检 200 + `OK`，未列入来源预检 400
  `Disallowed CORS origin`。
- 库里的凭据列只有 `*_hash` / `code_challenge`（测试里有列名白名单守着）。

### 7.1 限流与真实客户端 IP（2026-11-04）

两端实现同一套语义（`backend/app/core/ratelimit.py` /
`backend-go/internal/api/ratelimit.go`）：滑动窗口，计数只在本进程。

| 场景 | 默认阈值 | 环境变量 |
| --- | --- | --- |
| 登录失败（同一 IP+邮箱） | 10 次 / 15 分钟 | `APP_RATE_LIMIT_LOGIN_PAIR` |
| 登录失败（同一 IP） | 60 次 / 15 分钟 | `APP_RATE_LIMIT_LOGIN_IP` |
| 发验证码（同一邮箱） | 5 次 / 小时 | `APP_RATE_LIMIT_CODE_EMAIL` |
| 发验证码（同一 IP） | 30 次 / 小时 | `APP_RATE_LIMIT_CODE_IP` |
| 两次发码最小间隔 | 60 秒 | `APP_RATE_LIMIT_CODE_COOLDOWN` |
| 验证码校验（同一邮箱） | 20 次 / 小时 | `APP_RATE_LIMIT_VERIFY_EMAIL` |
| 全域每 IP 请求速率 | 关闭（0） | `APP_RATE_LIMIT_RPS` |
| 总开关 | 开 | `APP_RATE_LIMIT_ENABLED` |

- 超限返回 `429`，信封 `code=rate_limited`、`details.retry_after`，并带
  `Retry-After` 头。
- 登录只在**失败**时计数，正常登录不受影响。
- 发码先按邮箱计数再查库：邮箱是否存在都得到同样的 429，不泄露账号状态。
- **真实客户端 IP**：`client_ip` 现在只在 TCP 对端属于 `APP_TRUSTED_PROXIES`
  时采信 `CF-Connecting-IP` / `X-Forwarded-For`（从右往左跳过受信代理）。
  生产 compose 把内网段设成受信代理，Caddy 后面的限流才按真实访客计数；
  直连（127.0.0.1、无转发头）行为不变。
- 多副本部署时限流阈值会按副本数放大；灰度期 Go 与 Python 同时在跑，
  实际阈值也是两者之和。需要全局精确计数时应换 Redis/数据库计数。
- 本地跑 `tests/parity/concurrency.py` 时要给两个实例加
  `APP_RATE_LIMIT_ENABLED=false`，否则并发注册会被发码冷却挡成 429。

### 8. 运维演练

- [x] 数据库备份与恢复：`pg_dump` → 临时库 → `pg_restore`，表数量与
      6 张核心表行数一致（`tests/parity/backup_restore.py`）
- [x] uploads 卷的备份与恢复：tar 打包 → 恢复到新卷 → 内容与属主逐个比对
      （`tests/parity/uploads_backup.py` 4/4）
- [x] 容器 `restart: always` 自愈：`docker compose config` 校验四个服务都带
      `restart: always`；实测杀掉工作进程后容器自动重启（`RestartCount 0 → 1`）
- [x] 日志轮转：compose 里四个服务都是 json-file `max-size: 10m` / `max-file: 3`
- [ ] 磁盘水位、时间同步：按下面的命令纳入日常巡检

### 8.1 运维命令备忘

```sh
# uploads 卷备份（生产卷，注意别把 --rm 容器里的路径写错）
docker run --rm -v challenge_uploads:/data -v "$PWD":/backup alpine \
  tar czf /backup/uploads-$(date +%F).tgz -C /data .
# 恢复：先停写（compose stop api go-api），再解到卷里
docker run --rm -v challenge_uploads:/data -v "$PWD":/backup alpine \
  tar xzf /backup/uploads-2026-11-04.tgz -C /data

# 磁盘与日志
df -h /var/lib/docker
docker system df
docker logs --tail 200 challenge-api-1

# 时间同步（Debian/Ubuntu）
timedatectl status
```

注意：服务器重启后 Docker 会按 `restart: always` 拉起全部容器，但 compose 的
`depends_on: service_healthy` 只管 `docker compose up`，不管守护进程重启。
数据库比 API 晚就绪时，Go 进程会因为连不上库退出并被策略重启，属正常收敛过程。

---

## P2：容量与代码质量

### 9. 容量评估（2026-11-04 本机 8 核 + 基准库）

- [x] 按活动峰值并发压测（`loadgen -n 2000 -c 50`，两端同一份基准数据）

  | 接口 | Go | Python |
  | --- | --- | --- |
  | `/companies` | 3,811 rps，p50 12.5 ms | **303 rps**，p50 158 ms（修复前 44 rps / 1.22 s） |
  | `/projects` | 5,084 rps，p50 9.5 ms | 475 rps，p50 101 ms |
  | `/exhibition/feed` | 4,797 rps，p50 9.5 ms | 328 rps，p50 146 ms |
  | `/votes` | 3,925 rps，p50 12.3 ms | 311 rps，p50 155 ms |

  SSE 200 条并发连接的首帧：Go p50 **67–99 ms**；Python p50 **2.25 s → 1.06 s**
  （修好事件循环阻塞后，仍约为 Go 的 10 倍）。

- [x] `pg_stat_statements` 看慢查询：没有单条慢 SQL（最慢均值 0.52 ms，
      命中率 100%），真正的问题是 **N+1**：
      `SELECT count(*) FROM memberships WHERE company_id = $1` 在一次
      `/companies` 压测里被调用 13 万次。已改成批量 `GROUP BY`（Python 的
      `/companies` 与 `/finder/companies`），每次请求 1 条，响应与 Go 逐字段一致。
- [x] 连接池与 `max_connections`：Postgres 默认 **100**。Go 写池 20
      （`APP_DB_MAX_CONNS`，读写同池），Python 引擎 `pool_size=10 + max_overflow=10`
      = 最多 20；**灰度期两者同时跑最坏 40 条，离上限很远**，不是阻塞项。
      实测全 Go 单独跑：`-c 50`、5,010 rps、20,000 请求 0 错误时，Postgres 侧
      连接峰值 **21**（20 条池 + 采样连接），查询亚毫秒，20 条足够撑住。

      **全 Go 之后按副本数算**：`副本数 × APP_DB_MAX_CONNS + 预留 ≤ 100`

      | Go 副本 | 每实例 20（现配置） | 每实例 12 |
      | --- | --- | --- |
      | 1 | 20 | 12 |
      | 2 | 40 | 24 |
      | 4 | 80 | 48 |
      | 5 | 100（顶格，无余量） | 60 |

      结论：不换配置就能跑 4 个副本；要上 5+ 就把 `APP_DB_MAX_CONNS` 调到
      12–15，或者加 PgBouncer。另注意读池与写池目前共用一份
      （`APP_READ_DATABASE_URL` 为空），将来挂只读副本后每实例会变成
      「20 写 + 20 读」，要一起算。
- [x] SSE 事件循环阻塞：Python 的 `/votes/stream` 原来在 async 生成器里直接跑同步
      查询，200 条连接时把事件循环卡住；已改为 `asyncio.to_thread`，首帧从
      2.25 s 降到 1.06 s。若要进一步追平 Go（几十毫秒），需要改成「一个后台轮询器
      + 向所有连接广播」，已记入 P4。

### 10. 静态检查

- [x] Python 侧补跑 `pytest` 与 `ruff`（2026-11-04：97 项全过，`ruff check app tests` 无告警）

### 11. 已知功能缺口（两个实现都有）

- [x] 定时广播只写 `scheduled` 状态，没有发送 worker —— 2026-11-04 已补，见 11.1
- ~~没有接口级限流~~（2026-11-04 已补，见 7.1）

### 11.1 定时广播 worker（2026-11-04）

两端各有一个进程内轮询任务（`backend/app/services/broadcast_worker.py` /
`backend-go/internal/api/broadcast_worker.go`），间隔由
`APP_BROADCAST_POLL_SECONDS` 控制（默认 30 秒，0 = 关闭）。

- [x] 到点自动发送：`scheduled` + `scheduled_at <= now` 的广播自动投递并置 `sent`
- [x] 先认领再发：条件更新 `scheduled → sending`，两个实例同时轮询也不会重复发
      （`tests/parity/broadcast_schedule.py` 23/23，含抢单场景）
- [x] 取消后不再发送；到点时没有收件人则记日志并置 `sent`（0 封）
- [x] 发送异常回滚认领，下一轮重试；卡在 `sending` 超过 1 小时的按已发送收尾
      （部分发送的情况看邮件日志，逐条状态都在 `email_messages`）

注意：`sending` 是新增的中间状态，后台列表里会短暂出现；不需要为它做前端适配，
它最多停留一轮轮询。多实例部署时每个实例都会轮询，靠认领保证只发一次。

---

## P3：上线前应该有（本文档原本未单列，2026-11-04 补记）

- [ ] 前端部署：`infra/Caddyfile` 目前仍是 `respond "frontend not deployed yet" 404`
- [ ] 监控与告警：只有日志轮转（10m×3），没有指标、告警与定时健康巡检
- [ ] 备份自动化：恢复演练已有，但没有定时备份任务与保留策略（数据库 + uploads 卷）
- [ ] 迁移到自有服务器 + 自有 Cloudflare 账号：DNS、证书、防火墙、WAF 规则整套搬
      （迁移前，本机与 47.236.14.84 上的 Cloudflare 相关验证结论都要重做）
      **2026-11-05 决定**：走「自建迷你主机 + Cloudflare Tunnel」，
      见 `docs/runbooks/deploy-selfhost.md`（纯 Go 端、16G/512G、restic 备份）；
      云服务器 + ICP 备案路线保留在 `docs/runbooks/deploy-cn.md`，正式对外时再评估。
      - [x] 仓库侧工具链已就绪（2026-11-04）：`scripts/cf_provision.py`
            （A 记录 + 15 年 Origin CA 证书，默认只读）、`scripts/cf_firewall.sh`
            （DOCKER-USER 只放行 Cloudflare 网段）、`infra/caddy/tls-origin.caddy`
            （compose 用 `TLS_MODE` 切换），步骤见
            `docs/runbooks/cloudflare-migration.md`
      - [ ] 买域名 → 加入本人 Cloudflare 账号 → 建 API Token → 填 `infra/.env.infra`
      - [ ] 阿里云大陆 ECS（包年包月 ≥ 3 个月）+ 学校主体 ICP 备案，
            域名持有者与备案主体一致、TLD 可备案（见 `deploy-cn.md` 第 0、4 节）
      - [ ] 在自有服务器上 `cf_provision.py apply` + `cf_firewall.sh --apply`
      - [ ] 橙云 + Origin CA 下复测真实客户端 IP、SSE、上传、邮件
      - [ ] 决定 WAF 是否继续拦非浏览器 UA（Bot Fight Mode 建议关闭）

## P4：可以更晚

- [ ] 对象存储驱动（目前只有本地磁盘，R2/S3 只留了接口）
- [x] 图片变体/缩略图管线：**2026-11-15 完成**。上传后转 webp 并生成 w1600/w960/w480，
      `/files/{id}/{variant}` 真正返回文件；GIF 与视频原样保存不处理
- [ ] 限流从进程内升级为 Redis/DB 全局计数（多副本或灰度期同时跑才需要）
- [x] SSE 改成「一个后台轮询器 + 广播给所有连接」：**2026-11-15 完成**。
      `internal/broker` 一个轮询器服务多连接，无订阅者时不查库
- [ ] 早期 20 个模块级对照脚本从 `%TEMP%\ubench` 迁进 `tests/parity/`
- [x] 上传文件生命周期：**2026-11-15 完成**。孤儿分片与超时未完成的素材定时清理，
      素材总占用可设上限（`APP_UPLOAD_QUOTA_BYTES`），超出返回 507

---

## 2026-11-15 补强项（需要真机复验）

代码已完成，但都还没有对着真实上游与真实数据库跑过：

- [ ] `/v1/chat/completions` 支持 `stream: true`：用真账号与真上游验证一次
      ```bash
      curl -N -H "Authorization: Bearer sk_xxx" -H "Content-Type: application/json" \
        -d '{"model":"<模型名>","stream":true,"messages":[{"role":"user","content":"你好"}]}' \
        http://127.0.0.1:8000/v1/chat/completions
      ```
      要确认：逐块输出、末尾拿到 `usage`、随后收到 `[DONE]`、`llm_usage` 表多一行
- [ ] 上游不支持 `include_usage` 时的拒绝路径：确认流结束时**不发 `[DONE]`**、
      而是发一条 `stream_usage_unavailable` 事件，且该上游后续流式请求被直接拒绝
- [ ] 图片上传后确认磁盘上出现 `w1600/w960/w480` 三个 webp，
      `media_assets.variants` 与 `width/height` 被写入

---

## 2026-11-17 赛季能力（需要真库复验）

**部署顺序**：先 `alembic upgrade head`，再换 `go-api` 二进制。新二进制所有事件查询都读
`events.archived_at`，先换二进制会在迁移前把事件相关接口全部打成 500。

- [ ] 迁移 `alembic upgrade head` 后确认 `events.archived_at` 与 `event_enrollments` 存在
- [ ] **导入旧数据后先确认当期赛事的 `ends_at` 已填**。收口只认 `ends_at`，
      为空就永远不会自动归档、也不会重置身份（`stage` 不参与判断）。
      旧站 2026 那一届的结束时间按实际闭幕时间补上
- [ ] 把当期赛事的 `ends_at` 临时改到 31 天前，等一轮 `seasonLoop`（间隔默认 3600 秒，
      联调时设 `APP_SEASON_POLL_SECONDS=60`），确认：
      - `events.archived_at` 被写入、`stage='archived'`、报名与投票开关关闭
      - `GET /api/v1/exhibition/archive` 出现这一届，画廊变为空数组
      - 原本是 `member` / `captain` 的账号变成 `participant`，`staff` / `admin` 不变
      - `audit_log` 多出 `event.archive` 与 `event.offseason_reset` 两条，`actor_id` 为空
- [ ] 恢复 `ends_at` 后确认收口**不会回滚**（`archived_at` 是终态，需要人工清空才重来）
- [ ] 报名链路：`registration_open=false` 时报名返回 409；打开后分别报 `participant`
      与 `audience`，确认 `users.role` 随之升降，且管理员报 `participant` 不被降/升
- [ ] 多届隔离：建两届各一家同名成员的公司，确认
      `GET /projects/mine`、`GET /usage/team`、广告发布都只认当期那一届

### 赛后继续提交（2026-11-17 追加）

- [ ] 把当期 `ends_at` 改到过去，用队长账号发布一版新版本，确认仍返回 200
- [ ] 等到收口（或手工写 `archived_at`）之后再次发布，仍然 200——`post_event_edits` 默认开
- [ ] 后台 `PUT /api/v1/admin/event/switches {"post_event_edits": false}` 之后，
      保存草稿、发布、回滚全部返回 409；重新打开后恢复
- [ ] `GET /api/v1/exhibition/archive/{event_id}` 返回的 `live_version` 与
      `competition_version` 在赛后有新发布时确实不同

---

## 建议顺序

1. ~~空库冷启动~~（已完成）
2. ~~上传权限修复与实测~~（已完成）
3. ~~真实邮件驱动与业务全链路~~（已完成）
4. ~~Caddy 灰度与回滚演练~~（本机与真实域名 + TLS 均已通过）
5. 并发与安全扫描已完成；容量评估见第 9 节

---

## 测试资产

- `tests/parity/` 下有十个可重复执行的套件（并发、权限、限流、定时广播、上传安全、
  安全响应头、业务邮件、邮件重发、数据库备份恢复、uploads 卷备份恢复），
  用法见其中的 `README.md`；它们需要本地同时起着 Go 与 Python 两个实例。
- 早期 20 个模块级对照脚本仍在开发机的 `%TEMP%\ubench`（未入库，临时目录可能被
  清理），建议后续也迁到 `tests/parity/` 并去掉本机绝对路径。
- 压测工具：`backend-go/tools/loadgen`，支持 `-header "K: V"`，非 2xx 计为错误。
- 邮件自检：`backend-go/tools/mailprobe -to you@example.com`。
- 本地联调命令见 `backend-go/README.md` 的「本地联调」一节。
