"""并发写一致性测试（同一数据库，分别打 Go 与 Python）。

覆盖：
1. 同一邮箱并发注册 —— 不能出现 5xx
2. 同一作品两人抢编辑锁 —— 恰好一个 200、一个 409
3. 同一用户并发投票 —— 不超过钱包上限，不能出现 5xx
4. 同名公司并发创建 —— slug 不冲突，不能出现 5xx

用法（两个实例都要关掉限流，否则并发注册会被发码冷却挡成 429）：
    APP_RATE_LIMIT_ENABLED=false
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/concurrency.py
"""

import http.cookiejar
import json
import os
import threading
import urllib.error
import urllib.request
import uuid

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
PASSWORD = os.environ.get("BENCH_PASSWORD", "bench-password-123")

# 夹具（来自基准库）：同一作品的两个成员、无队伍无投票的参赛者
PROJECT_ID = os.environ.get("LOCK_PROJECT", "01681cfbd61245158d9bb3ebc869daa6")
LOCK_USERS = os.environ.get("LOCK_USERS", "user2@bench.example.com,user62@bench.example.com").split(",")
VOTER = os.environ.get("VOTER", "cw-1793711395-2@bench.example.com")
CREATORS = os.environ.get(
    "CREATORS", "cw-1793711395-0@bench.example.com,cw-1793711395-2@bench.example.com").split(",")

checks = []


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
        except Exception:
            return exc.code, {}


def login(base, email):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, _ = call(op, base, "/api/v1/auth/sessions",
                     {"email": email, "password": PASSWORD}, "POST")
    assert status == 200, (base, email, status)
    return op


def race(calls):
    """同时发起，返回状态码列表。"""
    barrier = threading.Barrier(len(calls))
    results = [None] * len(calls)

    def worker(index, fn):
        barrier.wait()
        try:
            results[index] = fn()
        except Exception as exc:  # noqa: BLE001
            results[index] = f"EXC {type(exc).__name__}: {exc}"

    threads = [threading.Thread(target=worker, args=(i, fn)) for i, fn in enumerate(calls)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return results


def test_double_registration(base):
    email = f"race-{uuid.uuid4().hex[:10]}@bench.example.com"
    payload = {"email": email, "password": "race-password-123",
               "display_name": "并发注册", "locale": "zh"}
    openers = [urllib.request.build_opener() for _ in range(6)]
    statuses = race([
        (lambda op=op: call(op, base, "/api/v1/auth/registrations", payload, "POST")[0])
        for op in openers
    ])
    check("并发注册无 5xx", all(isinstance(s, int) and s < 500 for s in statuses), str(statuses))
    check("并发注册结果在 202/409 内",
          all(s in (202, 409) for s in statuses), str(statuses))

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("DELETE FROM verification_codes WHERE email = %s", (email,))
        conn.execute("DELETE FROM users WHERE email = %s", (email,))


def test_edit_lock(base):
    first = login(base, LOCK_USERS[0])
    second = login(base, LOCK_USERS[1])
    path = f"/api/v1/projects/{PROJECT_ID}/edit-sessions"
    statuses = race([
        (lambda op=first: call(op, base, path, {"take_over": False}, "POST")[0]),
        (lambda op=second: call(op, base, path, {"take_over": False}, "POST")[0]),
    ])
    check("抢编辑锁无 5xx", all(isinstance(s, int) and s < 500 for s in statuses), str(statuses))
    check("恰好一个拿到锁", sorted(statuses) == [200, 409], str(statuses))

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("DELETE FROM project_edit_sessions WHERE project_id = %s", (PROJECT_ID,))


def test_concurrent_votes(base):
    op = login(base, VOTER)
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("DELETE FROM votes WHERE voter_id = (SELECT id FROM users WHERE email = %s)",
                     (VOTER,))
        candidates = [row[0] for row in conn.execute(
            "SELECT id FROM companies ORDER BY id LIMIT 8").fetchall()]

    statuses = race([
        (lambda cid=cid: call(op, base, f"/api/v1/me/votes/{cid}", None, "PUT")[0])
        for cid in candidates
    ])
    check("并发投票无 5xx", all(isinstance(s, int) and s < 500 for s in statuses), str(statuses))
    accepted = sum(1 for s in statuses if s == 200)
    check("并发投票未超上限（≤3）", accepted <= 3, f"accepted={accepted}")

    status, body = call(op, base, "/api/v1/me/votes")
    votes = body.get("votes", []) if isinstance(body, dict) else []
    check("最终票数一致（≤3）", status == 200 and len(votes) <= 3, f"n={len(votes)}")

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("DELETE FROM votes WHERE voter_id = (SELECT id FROM users WHERE email = %s)",
                     (VOTER,))


def test_company_creation(base):
    name = f"并发测试队-{uuid.uuid4().hex[:6]}"
    openers = [login(base, email) for email in CREATORS]
    statuses = race([
        (lambda op=op: call(op, base, "/api/v1/companies", {"name": name}, "POST")[0])
        for op in openers
    ])
    check("并发建队无 5xx", all(isinstance(s, int) and s < 500 for s in statuses), str(statuses))
    check("并发建队成功或冲突", all(s in (201, 409) for s in statuses), str(statuses))

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        slugs = [row[0] for row in conn.execute(
            "SELECT slug FROM companies WHERE name = %s", (name,)).fetchall()]
        check("slug 唯一", len(slugs) == len(set(slugs)), str(slugs))
        conn.execute(
            "DELETE FROM memberships WHERE company_id IN "
            "(SELECT id FROM companies WHERE name = %s)", (name,))
        conn.execute("DELETE FROM companies WHERE name = %s", (name,))
        conn.execute("UPDATE users SET role = 'participant' WHERE email = ANY(%s)",
                     (CREATORS,))


def run(base, label):
    print(f"\n=== {label} ({base}) ===")
    test_double_registration(base)
    test_edit_lock(base)
    test_concurrent_votes(base)
    test_company_creation(base)


run(GO_BASE, "Go")
run(PY_BASE, "Python")

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
