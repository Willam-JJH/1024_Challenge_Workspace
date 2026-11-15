"""定时广播：到点自动发送、取消后不发、两个实例抢同一广播不会重复发。

需要两个实例都开着 worker，并把轮询间隔调小：
    APP_BROADCAST_POLL_SECONDS=2

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    APP_BROADCAST_POLL_SECONDS=2 python tests/parity/broadcast_schedule.py
"""

import http.cookiejar
import json
import os
import time
import urllib.error
import urllib.request
import uuid
from datetime import UTC, datetime, timedelta

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
ADMIN = ("admin@bench.example.com", os.environ.get("BENCH_PASSWORD", "bench-password-123"))
TIMEOUT = float(os.environ.get("SCHEDULE_TIMEOUT", "25"))

checks = []
created: list[str] = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def call(op, base, path, payload=None, method="GET"):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"} if data else {}
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    try:
        with op.open(request, timeout=30) as response:
            raw = response.read()
            return response.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            return exc.code, json.loads(raw or b"null")
        except Exception:  # noqa: BLE001
            return exc.code, {}


def login(base):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, _ = call(op, base, "/api/v1/auth/sessions",
                     {"email": ADMIN[0], "password": ADMIN[1]}, "POST")
    assert status == 200, (base, status)
    return op


def staff_count() -> int:
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        return conn.execute(
            "SELECT count(*) FROM users WHERE status = 'active' "
            "AND role IN ('staff', 'admin')").fetchone()[0]


def messages_of(broadcast_id: str):
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        return conn.execute(
            "SELECT to_email, status FROM email_messages WHERE broadcast_id = %s",
            (broadcast_id,)).fetchall()


def status_of(broadcast_id: str):
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        return conn.execute(
            "SELECT status, sent_at FROM broadcasts WHERE id = %s",
            (broadcast_id,)).fetchone()


def wait_for(broadcast_id: str, wanted: str, timeout: float = TIMEOUT):
    deadline = time.time() + timeout
    while time.time() < deadline:
        row = status_of(broadcast_id)
        if row is not None and row[0] == wanted:
            return row
        time.sleep(0.5)
    return status_of(broadcast_id)


def create(op, base, title: str):
    status, body = call(op, base, "/api/v1/admin/email/broadcasts",
                        {"title": title, "body": "你好 {name}", "audience": "staff",
                         "locale": "all"}, "POST")
    assert status == 201, (status, body)
    created.append(body["id"])
    return body["id"]


def schedule(op, base, broadcast_id: str, when: datetime):
    status, body = call(op, base, f"/api/v1/admin/email/broadcasts/{broadcast_id}/schedules",
                        {"scheduled_at": when.isoformat()}, "POST")
    return status, body


recipients = staff_count()
print(f"--- staff 收件人 {recipients} 人 ---")

for label, base in (("Go", GO_BASE), ("Python", PY_BASE)):
    op = login(base)

    print(f"\n=== {label} 到点发送 ===")
    broadcast_id = create(op, base, f"定时测试 {label} {uuid.uuid4().hex[:6]}")
    status, body = schedule(op, base, broadcast_id, datetime.now(UTC) + timedelta(seconds=3))
    check(f"{label} 排期接口 200", status == 200, f"{status}")
    check(f"{label} 排期后状态 scheduled", body.get("status") == "scheduled",
          str(body.get("status")))

    row = wait_for(broadcast_id, "sent")
    check(f"{label} 到点自动置为 sent", row is not None and row[0] == "sent", str(row))
    check(f"{label} sent_at 已写入", row is not None and row[1] is not None)
    rows = messages_of(broadcast_id)
    check(f"{label} 每人一条日志", len(rows) == recipients, f"{len(rows)} vs {recipients}")
    check(f"{label} 全部投递成功", all(r[1] == "sent" for r in rows),
          str(sorted({r[1] for r in rows})))

    print(f"\n=== {label} 取消后不再发送 ===")
    broadcast_id = create(op, base, f"取消测试 {label} {uuid.uuid4().hex[:6]}")
    status, body = schedule(op, base, broadcast_id, datetime.now(UTC) + timedelta(seconds=90))
    check(f"{label} 取消前排期 200", status == 200, f"{status}")
    status, _ = call(op, base, f"/api/v1/admin/email/broadcasts/{broadcast_id}/cancellations",
                     {}, "POST")
    check(f"{label} 取消接口 200", status == 200, f"{status}")
    time.sleep(4)
    row = status_of(broadcast_id)
    check(f"{label} 取消后仍是 cancelled", row is not None and row[0] == "cancelled", str(row))
    check(f"{label} 取消后没有邮件日志", not messages_of(broadcast_id))

print("\n=== 两个实例抢同一个到点广播 ===")
race_id = None
with psycopg.connect(DB_DSN, autocommit=True) as conn:
    event_id = conn.execute("SELECT id FROM events ORDER BY created_at LIMIT 1").fetchone()[0]
    race_id = uuid.uuid4().hex
    conn.execute(
        "INSERT INTO broadcasts (id, event_id, title, body, audience, locale, status, "
        "scheduled_at, created_at, updated_at) "
        "VALUES (%s, %s, %s, %s, 'staff', 'all', 'scheduled', %s, now(), now())",
        (race_id, event_id, f"抢单测试 {race_id[:6]}", "你好 {name}",
         datetime.now(UTC) - timedelta(seconds=1)))
created.append(race_id)

row = wait_for(race_id, "sent")
check("抢单后状态 sent", row is not None and row[0] == "sent", str(row))
rows = messages_of(race_id)
check("只发一轮（人数与日志数一致）", len(rows) == recipients, f"{len(rows)} vs {recipients}")
dupes = len(rows) - len({r[0] for r in rows})
check("没有重复收件人", dupes == 0, f"duplicates={dupes}")

with psycopg.connect(DB_DSN, autocommit=True) as conn:
    for broadcast_id in created:
        conn.execute("DELETE FROM email_messages WHERE broadcast_id = %s", (broadcast_id,))
        conn.execute("DELETE FROM broadcasts WHERE id = %s", (broadcast_id,))

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
