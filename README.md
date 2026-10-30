# 1024 Challenge Workspace

1024 挑战赛平台的**新后端**：接口契约先行，前端后续按同一套契约重新设计。

## 这个仓库从哪来

功能来自原 `JaaamesZhang/Unscripted` 仓库（2026-10-15 之后的工作由本人完成，共 139 个提交、
约 1.7 万行手写代码）。原仓库根目录没有 LICENSE，代码默认「保留所有权利」，因此这里
**不复制实现**：接口按新的规范重新设计，代码、数据模型、迁移全部重写。

原仓库的 `apps/intern` 是 opencode（MIT）的 fork，未纳入本仓库。

## 现在的状态

| 部分 | 状态 |
|---|---|
| 接口契约 | **290 个端点全部注册**，见 [`docs/api/openapi.json`](docs/api/openapi.json) |
| 已实现 | 账号全流程（注册/邮箱验证/激活/口令重置/人机校验）、会话、我的资料与 API key、公司（组建/成员/邀请/入队申请/邀请链接）、队友集市、作品（草稿/编辑锁/发布/版本/出席）、赛事读取 |
| 其余端点 | 返回 `501 not_implemented`，在 OpenAPI 中可见路径、方法与摘要，按模块逐个补齐 |
| 数据模型 | users / sessions / api_keys / events / companies / memberships / projects / project_versions / votes / audit_log |
| 前端 | 目录占位，等接口稳定后重新设计 |
| 基础设施 | Docker Compose + Postgres 17 + Caddy；Cloudflare 需换成你自己的账号（见 `infra/cloudflare/README.md`） |

旧接口清单（267 个，仅作功能对照）见 [`docs/api/legacy-endpoints.md`](docs/api/legacy-endpoints.md)。

## 目录

```
backend/     FastAPI + SQLAlchemy 2.0 + Alembic
  app/core/    配置、数据库、鉴权、错误信封、分页
  app/models/  ORM 模型
  app/api/v1/  业务接口（按领域分模块）
  app/api/admin/ 管理台接口
  app/api/llm.py OpenAI 兼容网关面（挂在 /v1）
  migrations/  Alembic 迁移
  tests/       pytest
frontend/    占位（重设计后填充）
infra/       docker-compose、Caddyfile、Cloudflare 说明
docs/        api/（规范、OpenAPI、旧清单）、adr/
scripts/     导出 OpenAPI、生成旧接口清单
```

## 快速开始

```bash
cd backend
uv sync
uv run alembic upgrade head          # 或用 docker-compose 起 Postgres
uv run uvicorn app.main:app --reload # http://127.0.0.1:8000/docs
uv run pytest
```

默认使用本地 SQLite（`backend/dev.db`）；把 `APP_DATABASE_URL` 指到 Postgres 即可切换。

## 接口规范

- 前缀 `/api/v1`；LLM 网关单独挂在 `/v1`
- 鉴权：浏览器 `sid` HttpOnly cookie；程序 `Authorization: Bearer`（API key 前缀 `sk_`）
- 错误统一信封、游标分页、`If-Match` 乐观并发
- 细节见 [`docs/api/conventions.md`](docs/api/conventions.md)

## 署名与许可

- 本站是独立实现，不包含原仓库代码。
- 若后续引入任何第三方代码，其许可证与 NOTICE 必须原样保留。
