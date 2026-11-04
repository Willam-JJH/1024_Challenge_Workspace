"""权限矩阵：匿名/participant/member/captain/staff/admin 访问后台接口。

要求两点：每种角色得到预期状态码，且 Go 与 Python 完全一致。

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/permissions.py
"""

import http.cookiejar
import json
import os
import urllib.error
import urllib.request

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
PASSWORD = os.environ.get("BENCH_PASSWORD", "bench-password-123")

# member 角色没有现成可登录的账号，用参赛者临时改角色当夹具，跑完恢复
MEMBER_FIXTURE = "cw-1793711395-2@bench.example.com"
ACCOUNTS = {
    "participant": "cw-1793711395-0@bench.example.com",
    "member": MEMBER_FIXTURE,
    "captain": "cw-1793711395-1@bench.example.com",
    "staff": "staff@bench.example.com",
    "admin": "admin@bench.example.com",
}

GET_ENDPOINTS = [
    "/api/v1/admin/dashboard",
    "/api/v1/admin/users",
    "/api/v1/admin/audit",
    "/api/v1/admin/event",
]
WRITE_ENDPOINT = "/api/v1/admin/email/broadcasts"

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def call(op, base, path, payload=None, method="GET"):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"} if data else {}
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    try:
        with op.open(request, timeout=20) as response:
            response.read()
            return response.status
    except urllib.error.HTTPError as exc:
        exc.read()
        return exc.code


def login(base, email):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status = call(op, base, "/api/v1/auth/sessions",
                  {"email": email, "password": PASSWORD}, "POST")
    assert status == 200, (base, email, status)
    return op


def collect(base):
    results = {}
    anonymous = urllib.request.build_opener()
    for path in GET_ENDPOINTS:
        results[("anonymous", path)] = call(anonymous, base, path)
        results[("anonymous", WRITE_ENDPOINT)] = call(
            anonymous, base, WRITE_ENDPOINT, {}, "POST")
    for role, email in ACCOUNTS.items():
        op = login(base, email)
        for path in GET_ENDPOINTS:
            results[(role, path)] = call(op, base, path)
        # 空 body：staff/admin 应得到 422，其余角色在鉴权阶段就被拒
        results[(role, WRITE_ENDPOINT)] = call(op, base, WRITE_ENDPOINT, {}, "POST")
    return results


expected = {}
for path in GET_ENDPOINTS:
    expected[("anonymous", path)] = 401
    for role in ("participant", "member", "captain"):
        expected[(role, path)] = 403
    for role in ("staff", "admin"):
        expected[(role, path)] = 200
expected[("anonymous", WRITE_ENDPOINT)] = 401
for role in ("participant", "member", "captain"):
    expected[(role, WRITE_ENDPOINT)] = 403
for role in ("staff", "admin"):
    expected[(role, WRITE_ENDPOINT)] = 422

with psycopg.connect(DB_DSN, autocommit=True) as conn:
    conn.execute("UPDATE users SET role = 'member' WHERE email = %s", (MEMBER_FIXTURE,))

try:
    go = collect(GO_BASE)
    py = collect(PY_BASE)
finally:
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("UPDATE users SET role = 'participant' WHERE email = %s", (MEMBER_FIXTURE,))

for key in sorted(expected):
    want = expected[key]
    got_go, got_py = go.get(key), py.get(key)
    check(f"{key[0]:<12} {key[1]}", got_go == want and got_py == want,
          f"want={want} go={got_go} py={got_py}")

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
