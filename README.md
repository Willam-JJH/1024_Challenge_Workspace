# 1024 Challenge Workspace

1024 挑战赛平台：接口契约先行，后端用 **Go** 重写，前端后续按同一套契约重新设计。

## 这个仓库从哪来

功能来自原 `JaaamesZhang/Unscripted` 仓库（2026-10-15 之后的工作由本人完成，共 139 个提交、
约 1.7 万行手写代码）。原仓库根目录没有 LICENSE，代码默认「保留所有权利」，因此这里
**不复制实现**：接口按新的规范重新设计，代码、数据模型、迁移全部重写。

原仓库的 `apps/intern` 是 opencode（MIT）的 fork，未纳入本仓库；新版客户端是独立仓库。

## 现在的状态

| 部分 | 状态 |
|---|---|
| 接口契约 | **286 个端点**，见 [`docs/api/openapi.json`](docs/api/openapi.json)；新增部分在 [`docs/api/openapi-additions.json`](docs/api/openapi-additions.json) |
| 已实现 | **286 / 286，全部由 Go 实现**（[`backend-go/`](backend-go/)）：账号、公司、作品、投票、展览、直播、线下场次、评审、管理台、LLM 网关、邮件中心、OAuth、赛季与报名 |
| 数据模型 | 44 张表；Python 侧见 `backend/migrations`，Go 部署用 [`backend-go/internal/migrate/sql`](backend-go/internal/migrate/sql) |
| 前端 | 目录占位，等新设计稿 |
| 部署 | 自建迷你主机 + Cloudflare Tunnel（纯 Go、内置迁移、restic 备份），见 [`docs/runbooks/deploy-selfhost.md`](docs/runbooks/deploy-selfhost.md) |
| Python 实现 | `backend/` 已退出运行，仅作行为参照；首次部署验证通过后删除 |

旧接口清单（267 个，仅作功能对照）见 [`docs/api/legacy-endpoints.md`](docs/api/legacy-endpoints.md)。
接手这个仓库请先读 [`CLAUDE.md`](CLAUDE.md) 和
[`docs/runbooks/migration-status.md`](docs/runbooks/migration-status.md)。

## 目录

```
backend-go/   Go 后端（唯一在用的实现）
  cmd/api/      serve / migrate / create-admin 三个入口
  internal/     api 路由与 handler、auth、db、media、broker、migrate
  tools/        genstubs（501 路由表）、loadgen、mailprobe
backend/      FastAPI + SQLAlchemy + Alembic（退场中，仅作参照与迁移来源）
frontend/     占位（新设计稿完成后填充）
infra/        docker-compose、Caddyfile、Cloudflare 说明
docs/         api/（规范、OpenAPI）、design/（Intern、赛季）、runbooks/（迁移、部署、上线清单）
scripts/      导出 OpenAPI 与迁移 SQL、备份、Cloudflare 工具
tests/parity/ 跨实现对照测试（需要同时起 Python 与 Go，Python 删除后退役）
```

## 快速开始

```bash
# Go 后端（本机工具链在 C:\Users\willi\tools\go，不在 PATH）
cd backend-go
go build -o bin/api.exe ./cmd/api
APP_DATABASE_URL='postgres://app:app@127.0.0.1:5432/challenge' \
APP_PORT=18080 ./bin/api.exe          # migrate 子命令建表，见 README

# 本地数据库：docker compose -f infra/docker-compose.yml up -d db
# 建表：./bin/api migrate
```

## 接口规范

- 前缀 `/api/v1`；LLM 网关单独挂在 `/v1`
- 鉴权：浏览器 `sid` HttpOnly cookie；程序 `Authorization: Bearer`（API key 前缀 `sk_`）
- 错误统一信封、游标分页、`If-Match` 乐观并发
- 细节见 [`docs/api/conventions.md`](docs/api/conventions.md)

## 署名与许可

- 本站是独立实现，不包含原仓库代码。
- 若后续引入任何第三方代码，其许可证与 NOTICE 必须原样保留。
