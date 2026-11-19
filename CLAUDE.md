# 1024 Challenge Workspace

赛事平台：接口契约先行，后端 **Go** 实现，前端待按设计稿重做。
这个文件在项目目录启动 Claude Code 时自动加载，只写「每次都要知道」的事，
细节都在下面列出的文档里。

## 先读哪几份

| 想知道 | 读 |
| --- | --- |
| 整体进度、时间线、待办 | [`docs/runbooks/migration-status.md`](docs/runbooks/migration-status.md) |
| 上线前还差什么 | [`docs/runbooks/go-live-checklist.md`](docs/runbooks/go-live-checklist.md) |
| 赛季模型（收口 / 报名 / 前序作品 / 赛后提交） | [`docs/design/season-lifecycle.md`](docs/design/season-lifecycle.md) |
| 部署（自建迷你主机 + Cloudflare Tunnel） | [`docs/runbooks/deploy-selfhost.md`](docs/runbooks/deploy-selfhost.md) |
| 接口规范 | [`docs/api/conventions.md`](docs/api/conventions.md) |
| Intern 桌面客户端 | [`docs/design/intern-redesign.md`](docs/design/intern-redesign.md)（代码在独立仓库 `D:\code\intern`） |

## 现状（2026-11-19）

- **契约 286/286 全部由 Go 实现**（`backend-go/`），这是唯一在用的后端。
- Python 实现 `backend/` **已退出运行**，只作为行为参照保留；首次部署验证通过后删除
  （见 migration-status 第 4 节）。在那之前，`scripts/export_openapi.py` 与
  `scripts/export_schema_sql.py` 仍依赖它。
- 部署目标：**自建迷你主机 + Cloudflare Tunnel**，硬件未到货，**尚未上线**。
  compose / Caddyfile / 变量模板 / 内置迁移 / 备份脚本都已写好并在本地实测过。
- 前端目录只是占位，等设计稿。
- 原站数据已取回 `D:\backups\unscripted\pull-2026-11-15\`（840 MB，验证可恢复），
  可读的作品归档在 `D:\backups\unscripted\works-archive-2026-11-15\`，**尚未导入新库**。

## 硬约定

- **没说「发版」就不上线任何东西**：只在本机跑，不部署、不打 tag、不推生产。
- **绝不提交、打印或记录密钥**：`.env.local`、`infra/.env.prod`、API key、OTP、密码。
  需要用程序读，不要 `cat`。
- **只暂存显式路径**：`git add <具体文件>`，不要 `git add -A` 或 `git add .`。
- 提交信息用中文，末尾空一行后写 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。
- 改表只加列 / 加表；**已应用的迁移文件不回头改**，新增一个
  `backend-go/internal/migrate/sql/NNNN_*.sql`。
- 前端（还没写）不要硬编码颜色、字体、间距与面向用户的文案。

## 本机环境

| 项 | 值 |
| --- | --- |
| Go | `C:\Users\willi\tools\go\bin`（不在 PATH，命令前先 export） |
| Python（退场中） | `backend/.venv/Scripts/python.exe` |
| 本地测试库 | `docker start ubench-pg` → `app/app@127.0.0.1:15432`，用完 `docker stop` |
| Postgres | 保持运行，不要随手停 |

```bash
cd backend-go && go build ./... && go test -count=1 ./... && go vet ./...
cd backend && .venv/Scripts/python.exe -m pytest -q          # 97 项，只测 Python
cd backend-go && ./bin/api migrate                            # 建表/升级
backend/.venv/Scripts/python.exe scripts/export_schema_sql.py --check   # SQL 与迁移链一致性
```

已知噪音：`gofmt -l` 会报 `internal/auth/store.go`（历史 CRLF 问题，别动）；
`go test` 偶发 `cannot find intern-res.syso` 时加 `-count=1` 重跑。

## 下一步

1. mini PC 到货 → 按 `deploy-selfhost.md` 第 7 节部署（**先迁移再换二进制**）。
2. 导入旧数据前，确认当期赛事的 `ends_at` 已填，否则赛季收口与身份回落都不会触发。
3. 前端按新设计稿重做（三栏工作台之类先不用管，网站侧还没开始）。
4. 首次部署与恢复演练都通过后，删除 `backend/`、`tests/parity/`、
   `scripts/export_schema_sql.py`，`openapi.json` 与迁移 SQL 转手工维护。
