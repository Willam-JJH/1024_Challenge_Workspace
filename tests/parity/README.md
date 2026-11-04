# 跨实现对照测试

这些脚本需要**同一份数据库**上同时起着 Python（`backend/`）与 Go（`backend-go/`）两个实例，
然后对两边发同样的请求，比对状态码、响应体与最终落库结果。

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
| `concurrency.py` | 并发注册 / 抢编辑锁 / 并发投票 / 并发建队 | 用到夹具账号与作品，默认值见文件头 |
| `permissions.py` | 匿名与五种角色访问后台接口 | 会临时把 `cw-1793711395-2` 改成 member，跑完恢复 |
| `mail_resend.py` | 广播失败落库 → SMTP 恢复 → 重发 | 两个实例的 SMTP 要指向 `127.0.0.1:2526`（脚本会自己起假服务） |
| `backup_restore.py` | `pg_dump` → 临时库 → `pg_restore` → 行数比对 | 需要本地 `ubench-pg` 容器 |

```sh
GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
python tests/parity/concurrency.py

GO_BASE=... PY_BASE=... python tests/parity/permissions.py
GO_FAIL=http://127.0.0.1:18085 PY_FAIL=http://127.0.0.1:18003 python tests/parity/mail_resend.py
python tests/parity/backup_restore.py
```

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
