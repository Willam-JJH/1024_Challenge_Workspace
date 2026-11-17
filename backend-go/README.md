# backend-go

Go 版后端，目标是在不中断服务的前提下逐模块替换 `backend/`（FastAPI 实现）。

## 现状

- 286 个 OpenAPI operation 已全部注册路由：未实现的统一返回 `501 not_implemented`，
  错误信封与 Python 实现一致。其中 7 个（赛季与报名）只在 Go 侧实现，契约写在
  `docs/api/openapi-additions.json`。
- 已完成：配置（`APP_` 前缀）、读写连接池分离、请求 ID、CORS、访问日志、panic 兜底；
  **鉴权底座**（scrypt 校验、会话/API key、`sid` cookie、`last_seen` 节流）与
  `GET /me`、登录/退出接口；**公开只读接口**（events、projects 画廊、companies、
  exhibition 的 feed/promotions/comments、votes 榜单/大屏/**SSE**、pitch 状态/流源/留言）；
  **后台只读**（dashboard、users、audit、audit/actions、event）；
  **账号生命周期**（注册+邮箱验证码、重发、激活码验证、队友激活、改密、口令重置三段式、
  活跃会话列表与吊销、PoW 人机校验）；**作品写入**（草稿自动创建、编辑锁获取/续租/释放、
  自动保存与修订号冲突、发布与版本、版本回滚、出席意向、代理草稿复核）；
  **展览写入**（评论/回复/编辑/删除、举报、广告三条上限与增删改、打卡与护照、公司收章）；
  **后台账号管理写入**（建号、角色/方式/语言、停用与恢复、强制下线、重发激活、
  删除与批量操作、CSV 导出、审计写入）；**投票与结果**（投票看板、选票明细、
  公示开关、作废、入围名单公布/撤回、奖项公布/撤回）；**后台公司/成员/作品**
  （公司列表与档案、强制解散、名单锁、指定队长/提交人、成员移转、作品与版本查看）；
  **用户侧建队**（创建公司并成为队长、改名/简介、移除成员、退出、移交队长、指定提交人、名单锁）；
  **邀请与入队**（邮件邀请、重发、撤回、邀请链接与停用、入队申请、同意/拒绝）；
  **奖项日介绍与合作**（介绍任务列表、撰写介绍、被介绍方作答与删除、合作展示）；
  **线下场次与内容**（场次列表/详情、RSVP 与候补转正、iCalendar 导出、公告、赞助商、CSP 上报）；
  **媒体与评审**（素材申请/直传/分片/合并/元数据/替代文本/删除/读取、评分卡保存与提交）；
  **OAuth 与 LLM 网关**（授权服务器元数据、授权码 + PKCE S256、授权确认与撤销、令牌交换与刷新轮换、
  令牌吊销、userinfo、已连接应用与设备、用量汇总/明细/团队、`/v1/models`、`/v1/chat/completions`
  转发与计费、三个预留端点的 501；后台的预算、模型/上游/路由 CRUD、三档熔断开关、按人/按队用量）；
  **个人中心**（资料修改、API key 列表/创建/吊销/轮换、我的选票读写与撤票、公司关系、旁观/参赛切换、
  收到的邀请与接受/拒绝、入队申请撤回、通知与已读、邮件摘要偏好、参观印章、注销申请、介绍任务）；
  **邀请接收与集市**（邀请预览、邮件邀请接受/拒绝、邀请链接加入、队友集市公司/个人、结果与入围名单、
  后台命令面板搜索、链接预览与 GitHub 仓库卡片）；
  **后台赛事与内容**（日程与阶段、报名/投票/直播开关、素材下架、功能开关、
  公告的草稿/发布/编辑/删除、赞助商增删改与排序）；
  **后台线下场次**（场次列表/新建/编辑/删除、座位调整后的候补转正、取消并通知、
  报名名单、现场签到与撤销）；
  **直播页**（后台的标题与开播开关、流源增删改与上下移、留言审核；公开侧的文字留言、
  撤回自己的留言、3 秒推送的 `/pitch/stream` SSE）；
  **邮箱中心**（广播草稿/编辑/删除/取消/立即或定时发送、测试发送、按受众预览与规模估算、
  发送日志与失败重发、模板与模板预览、驱动与每日配额）；
  **后台展览**（总览、介绍任务的随机抽取/补分配/调整/换人/标记完成/发布/重置/一键揭晓、
  合作与评论审核列表、介绍与回复隐藏、奖项日签到、全部广告）。
- 与 Python 实现逐字段对齐：23 个读接口 0 差异；SSE 首帧一致。
- 断言：作品写入 14、展览写入 19、后台账号 22、投票与结果 15、公司/成员/作品 14、
  用户侧建队 13、邀请与入队 16、介绍与合作 9、离线与内容 10、媒体与评审 14、
  OAuth 与 LLM 网关 99、个人中心与集市 70、后台赛事与内容 39、后台线下场次 23、
  直播页 28、邮箱中心 36、后台展览 36，外加解散公司单独验证，全部通过
  （均包含 Python 读回一致）。
- 跨语言链路已实测：Python 签发验证码 → Go 验证并登录 → Python 认可该会话；
  Go 签发激活邀请 → Python 可登录；Python 出题+解题 → Go 校验通过（错解 422）；
  Go 授权的 code → Python 兑换成令牌 → Go userinfo 可用；Python 签发的 refresh →
  Go 轮换成功，旧 refresh 在两边都失效。
- 共完成 **279 / 279** 个 operation，契约全覆盖。
- **赛季能力**（2026-11-17）：一届一收口。`events.archived_at` 由后台任务在比赛结束满
  保留期（`APP_SEASON_CLOSE_AFTER_DAYS`，默认 30 天）时写入，同时关闭报名与投票、
  把 `member/captain` 回落为 `participant`；往届作品走 `/exhibition/archive`，
  画廊与实时动态只覆盖当期赛事；按届报名记在 `event_enrollments`。
  **赛后仍可继续提交作品新版本**（赛事级 `post_event_edits`，默认开），
  归档接口同时返回 `live_version` 与 `competition_version`。设计见
  `docs/design/season-lifecycle.md`。
- 顺手修掉一个线上隐患：handler 不读请求体时，连接关闭阶段在 Windows 上会间歇触发
  RST，客户端拿不到响应。已加 `withBodyBuffer`（≤1 MB 的 body 先读完再分发）。
- 顺手对齐一处行为：激活或接受邀请入队后，`participant` 会提升为 `member`
  （Python 的 `add_member` 一直是这样，Go 侧此前漏了）。
- 跨实现兼容已验证：Python 签发的 cookie 在 Go 侧可用，反之亦然；退出登录互通。
- 压测工具：`go run ./tools/loadgen -url <url> -n 3000 -c 20`。
- 已接入 `infra/docker-compose.prod.yml`（`go-api` 服务）与 Caddy 灰度规则；
  尚未在正式环境开放入口。

## 命令

Go 工具链装在本机 `C:\Users\willi\tools\go`，未加入系统 PATH。

```sh
# 生成 501 路由表（OpenAPI 变更后重跑）
go run ./tools/genstubs

go build -o bin/api.exe ./cmd/api
go test ./...
go vet ./...

# 本地运行（可以直接沿用 Python 侧的 APP_DATABASE_URL）
APP_DATABASE_URL='postgresql+psycopg://app:app@127.0.0.1:5432/challenge' \
APP_PORT=18080 ./bin/api.exe

# 邮件自检：用当前 APP_* 配置真实发一封（部署后验证 SMTP 用）
go run ./tools/mailprobe -to you@example.com
```

## 迁移约定

1. 契约以 `docs/api/openapi.json` 为准；新增 operation 先跑 `genstubs`。
2. 实现一个模块后，只改 `internal/api/server.go` 的 `implemented()`，把 pattern
   映射到 handler；`stubs_gen.go` 不需要手改。
3. 每个模块迁移时必须做对照测试：同一请求分别打 Python 与 Go（同一数据库副本），
   比对状态码、JSON 与响应头。
4. 切换用 Caddy 按路径前缀分流，任何一步都能回滚。

## 灰度切流（已准备，未上线）

`infra/Caddyfile` 里已把两个纯 GET 前缀指到 `go-api`：

```caddyfile
@goapi path /api/v1/events /api/v1/events/* /api/v1/votes /api/v1/votes/*
handle @goapi {
    reverse_proxy go-api:8000
}

handle /api/* {
    reverse_proxy api:8000
}
```

`infra/docker-compose.prod.yml` 增加了 `go-api` 服务，与 `api` 共用同一个数据库和 `APP_` 配置。

**回滚**：删掉 `@goapi` 与 `handle @goapi` 两块，重新加载 Caddy，流量立即回到 Python。

本地已验证（Caddy → 两个后端）：`events`/`votes`（含 SSE）命中 Go 日志，`pitch`/`projects`
走 Python，回滚配置下 `events` 回到 Python。

> 注意：仓库里原来的 Caddyfile 开头多了一个 `{`，`caddy validate` 会直接报错
> （线上服务器上的文件可能已手工改过）。提交前请以服务器上的实际文件为准做一次 diff。


## 本地联调（迁移期专用）

基准库是一个临时容器（Postgres 17 + pg_stat_statements，端口 15432），数据已灌好：

```sh
docker start ubench-pg

# Go 服务（上传目录用临时目录，避免和 Python 的 uploads 混淆）
APP_DATABASE_URL='postgresql+psycopg://app:app@127.0.0.1:15432/challenge' \
APP_PORT=18080 APP_UPLOAD_DIR='C:/Users/willi/AppData/Local/Temp/ubench/uploads' \
./bin/api.exe

# Python 服务（对照用）
cd ../backend
APP_DATABASE_URL=postgresql+psycopg://app:app@127.0.0.1:15432/challenge APP_ENVIRONMENT=dev \
PYTHONPATH=. .venv/Scripts/python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 18000 --workers 1
```

对照脚本在 `%TEMP%\ubench\e2e_*.py`（每个模块一个），用法是两边同时起着跑一遍，
输出 `PASS/FAIL` 列表；跑完会清理自己创建的数据。停止时记得 `docker stop ubench-pg`，
并结束 18000/18080 上的进程。



## 迁移完成，上线前检查清单

279 个 operation 全部实现，对照测试累计 477 项断言；本机全量回归、隔离测试实例的
冒烟与持续压测均已通过。**尚未开放公网入口，尚未正式上线。**

完整的测试与上线清单见
[`docs/runbooks/go-live-checklist.md`](../docs/runbooks/go-live-checklist.md)，
P0 阻塞项是：空库冷启动、素材上传、真实邮件通道、反向代理与回滚演练。

旧版简要清单（保留备查）：

1. 一次全量回归：把 `%TEMP%\ubench\e2e_*.py` 全部跑一遍，确认没有跨套件干扰。
2. 用 `tools/loadgen` 在接近生产的并发下压一遍关键路径（画廊、榜单、SSE、聊天转发）。
3. **diff 生产服务器上的 `/srv/.../infra/Caddyfile` 与 compose 文件**（仓库里的 Caddyfile
   曾经有语法错误，服务器上可能已手工改过）。
4. 决定切流顺序：先按 Caddy 的 `@goapi` 前缀灰度（events / votes），观察后再逐步扩大。
5. 回滚演练：删掉 `@goapi` 两块配置并 reload，确认流量回到 Python。

> 对照测试注意：Python 的 `get_db` 在 **响应发出之后** 才 `commit`（FastAPI 的 yield
> 依赖），跨实现“写一个、立刻读另一个”会读到旧快照。`%TEMP%\ubench` 里的脚本统一在
> 非 GET 请求后等 50 ms 再继续。

## 目录

| 路径 | 内容 |
| --- | --- |
| `cmd/api` | 入口与优雅退出 |
| `internal/api` | 路由、中间件、错误信封、各模块 handler |
| `internal/config` | `APP_` 环境变量 |
| `internal/db` | pgx 连接池（读写分离的扩展点） |
| `tools/genstubs` | 从 OpenAPI 生成 501 路由表 |
| `tools/loadgen` | 固定并发的压测工具 |
