# Go 迁移上线前检查清单

> 状态快照：2026-11-04。
> Go 后端已实现全部 279 个 OpenAPI operation，完成本机全量回归，并在一台隔离的
> 测试实例上做过冒烟与持续压测。**公网入口尚未开放，尚未正式上线。**
>
> P0-1（空库冷启动）与 P0-2（素材上传）已于 2026-11-04 在本机验证通过；
> P0-3（真实邮件）驱动完成并已在 VPS 上真实发信；P0-4 分流与回滚已在本机演练通过。
> 剩余 P0 工作：真实域名 + TLS 环境复测、邮件业务链路实测。

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
- [ ] 注册验证码、激活、口令重置、邀请逐条走真实 SMTP 实测

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
- [ ] 真实域名 + TLS 证书环境复测（本机只能用 `:80`）
- [ ] 若挂 Cloudflare：验证 `APP_BEHIND_CLOUDFLARE` / `APP_TRUSTED_PROXIES`
      下取到真实客户端 IP

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
- [ ] 登录暴力尝试、注册刷量（PoW 是否真的挡得住）
- [ ] 上传滥用（类型、大小、路径穿越）
- [ ] cookie `Secure` / `SameSite`、CORS 白名单、TLS、密钥不入库
- [ ] 没有接口级限流（两边都是；登录/注册建议在网关或应用层补）

### 8. 运维演练

- [x] 数据库备份与恢复：`pg_dump` → 临时库 → `pg_restore`，表数量与
      6 张核心表行数一致（`tests/parity/backup_restore.py`）
- [ ] uploads 卷的备份与恢复
- [ ] 容器 `restart: always` 自愈；服务器重启后全栈自动恢复
- [ ] 日志轮转、磁盘水位、时间同步

---

## P2：容量与代码质量

### 9. 容量评估

- [ ] 按活动峰值并发（SSE 连接数、画廊/榜单 QPS）压一轮
- [ ] `pg_stat_statements` 找慢查询，必要时补索引
- [ ] 确认 Go/Python 连接池与 Postgres `max_connections` 的关系

### 10. 静态检查

- [ ] Python 侧补跑 `pytest` 与 `ruff`（本轮只跑了 e2e 对照）

### 11. 已知功能缺口（两个实现都有）

- [ ] 定时广播只写 `scheduled` 状态，**没有发送 worker**
- [ ] 没有接口级限流；登录/注册建议在网关或应用层补

---

## 建议顺序

1. ~~空库冷启动~~（已完成）
2. ~~上传权限修复与实测~~（已完成）
3. 真实邮件驱动（已实现并真实发信；待跑注册/重置/邀请/广播全链路）
4. Caddy 灰度与回滚演练（本机已通过，待真实域名 + TLS 复测）
5. 并发、安全扫描、容量评估

---

## 测试资产

- `tests/parity/` 下有四个可重复执行的套件（并发、权限、邮件重发、备份恢复），
  用法见其中的 `README.md`；它们需要本地同时起着 Go 与 Python 两个实例。
- 早期 20 个模块级对照脚本仍在开发机的 `%TEMP%\ubench`（未入库，临时目录可能被
  清理），建议后续也迁到 `tests/parity/` 并去掉本机绝对路径。
- 压测工具：`backend-go/tools/loadgen`，支持 `-header "K: V"`，非 2xx 计为错误。
- 邮件自检：`backend-go/tools/mailprobe -to you@example.com`。
- 本地联调命令见 `backend-go/README.md` 的「本地联调」一节。
