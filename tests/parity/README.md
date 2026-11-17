# 跨实现对照测试

这些脚本需要**同一份数据库**上同时起着 Python（`backend/`）与 Go（`backend-go/`）两个实例，
然后对两边发同样的请求，比对状态码、响应体与最终落库结果。

> 2026-11-17 起新增的赛季与报名接口**只在 Go 侧实现**（见 `docs/design/season-lifecycle.md`），
> 没有对照脚本。它们的验证步骤写在 `docs/runbooks/go-live-checklist.md` 的
> 「2026-11-17 赛季能力」一节。

## 准备

```sh
# 基准库（本机临时容器）
docker start ubench-pg

# Go
cd backend-go
APP_DATABASE_URL='postgresql+psycopg://app:app@127.0.0.1:15432/challenge' \
APP_PORT=18080 APP_ENVIRONMENT=dev APP_CURRENT_EVENT_SLUG=1024-challenge \
./bin/api.exe

# Python
cd backend
APP_DATABASE_URL='postgresql+psycopg://app:app@127.0.0.1:15432/challenge' \
APP_ENVIRONMENT=dev PYTHONPATH=. \
.venv/Scripts/python.exe -m uvicorn app.main:app --host 127.0.0.1 --port 18000 --workers 1
```

基准库的测试账号口令统一是 `bench-password-123`。

## 套件

| 脚本 | 内容 | 额外要求 |
| --- | --- | --- |
| `concurrency.py` | 并发注册 / 抢编辑锁 / 并发投票 / 并发建队 | 用到夹具账号与作品，默认值见文件头；两个实例要关限流 |
| `permissions.py` | 匿名与五种角色访问后台接口 | 会临时把 `cw-1793711395-2` 改成 member，跑完恢复 |
| `ratelimit.py` | 登录失败锁定 / 发码冷却 / 校验次数上限 | 计数在进程内，重跑前先重启实例 |
| `broadcast_schedule.py` | 定时广播到点发送 / 取消 / 两个实例抢单 | 两个实例要设 `APP_BROADCAST_POLL_SECONDS=2` |
| `upload_security.py` | 文件名穿越 / 分片顺序 / 超限 / 下发类型 | 两个实例要设 `APP_MAX_UPLOAD_BYTES=1048576` |
| `security_headers.py` | cookie 属性 / CORS 白名单 / 密钥不入库 | 两个实例要设 `APP_COOKIE_SECURE=true` 与 `APP_CORS_ORIGINS` |
| `mail_resend.py` | 广播失败落库 → SMTP 恢复 → 重发 | 两个实例的 SMTP 要指向 `127.0.0.1:2526`（脚本会自己起假服务） |
| `mail_flows.py` | 注册 / 重置 / 邀请 / 广播四条业务邮件全链路 | 两个实例指向 `127.0.0.1:2527` 的假 SMTP，并设 `APP_RATE_LIMIT_CODE_COOLDOWN=0` |
| `backup_restore.py` | `pg_dump` → 临时库 → `pg_restore` → 行数比对 | 需要本地 `ubench-pg` 容器 |
| `uploads_backup.py` | uploads 卷 tar 备份 → 新卷恢复 → 内容与属主比对 | 用 `alpine` 容器，只动临时卷 |

```sh
GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
python tests/parity/concurrency.py

GO_BASE=... PY_BASE=... python tests/parity/permissions.py
GO_BASE=... PY_BASE=... python tests/parity/ratelimit.py
GO_BASE=... PY_BASE=... python tests/parity/broadcast_schedule.py
GO_BASE=... PY_BASE=... python tests/parity/upload_security.py
GO_BASE=... PY_BASE=... python tests/parity/security_headers.py
GO_FAIL=http://127.0.0.1:18085 PY_FAIL=http://127.0.0.1:18003 python tests/parity/mail_resend.py
GO_BASE=... PY_BASE=... python tests/parity/mail_flows.py
python tests/parity/backup_restore.py
python tests/parity/uploads_backup.py
```

`permissions.py` 用限流默认值就能跑；`concurrency.py` 里的并发注册会被
「同一邮箱 60 秒内只能发一次码」挡成 429，所以跑并发套件时两个实例都要加
`APP_RATE_LIMIT_ENABLED=false`；`ratelimit.py` 反过来要**开着**限流（默认即开）。

`mail_resend.py` 需要另起两个把 SMTP 指向空端口的实例：

```sh
APP_MAIL_DRIVER=smtp APP_SMTP_HOST=127.0.0.1 APP_SMTP_PORT=2526 \
APP_SMTP_TLS=none APP_SMTP_USER=fake APP_SMTP_PASSWORD=fake \
APP_MAIL_FROM=no-reply@bench.example.com ...
```

## 注意

- Windows Git Bash 里给容器传 `/…` 路径会被改写，涉及容器内路径时加
  `MSYS_NO_PATHCONV=1`；`backup_restore.py` 已改为走 stdin/stdout，不受影响。
- 并发套件会创建并清理临时数据；如果中途失败，按脚本结尾的清理语句手工收尾。
