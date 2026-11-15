"""会话 cookie、CORS 白名单、密钥不入库。

需要两个实例按生产口径配置：
    APP_COOKIE_SECURE=true
    APP_CORS_ORIGINS='["https://app.example.test"]'

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/security_headers.py
"""

import hashlib
import http.client
import json
import os
import re

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
GOOD_ORIGIN = os.environ.get("CORS_ORIGIN", "https://app.example.test")
BAD_ORIGIN = "https://evil.example"
USER = ("cw-1793711395-0@bench.example.com",
        os.environ.get("BENCH_PASSWORD", "bench-password-123"))

# 库里与凭据相关的列只能是哈希或 PKCE 挑战；出现明文列这条就会失败
CREDENTIAL_COLUMNS = {
    "invitations.token_hash",
    "invite_links.token_hash",
    "oauth_codes.code_challenge",
    "oauth_codes.code_hash",
    "project_edit_sessions.token_hash",
    "sessions.token_hash",
    "users.password_hash",
    "verification_codes.code_hash",
}

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def request(base, path, method="GET", payload=None, headers=None):
    host = base.split("//", 1)[1]
    conn = http.client.HTTPConnection(host, timeout=30)
    body = json.dumps(payload).encode() if payload is not None else None
    hdrs = dict(headers or {})
    if body is not None:
        hdrs["Content-Type"] = "application/json"
    try:
        conn.request(method, path, body=body, headers=hdrs)
        response = conn.getresponse()
        raw = response.read()
        got = {}
        for key, value in response.getheaders():
            got.setdefault(key.lower(), []).append(value)
        return response.status, raw, got
    finally:
        conn.close()


def login(base):
    status, raw, headers = request(
        base, "/api/v1/auth/sessions", "POST",
        {"email": USER[0], "password": USER[1]})
    assert status == 200, (base, status, raw[:200])
    return json.loads(raw), headers.get("set-cookie", [""])[0]


def run(base, label):
    print(f"\n=== {label} ({base}) ===")
    body, cookie = login(base)
    token = body["token"]

    lower = cookie.lower()
    check(f"{label} cookie HttpOnly", "httponly" in lower, cookie)
    check(f"{label} cookie Secure", "secure" in lower, cookie)
    check(f"{label} cookie SameSite=Lax", "samesite=lax" in lower, cookie)
    check(f"{label} cookie Path=/", "path=/" in lower, cookie)

    status, raw, headers = request(base, "/api/v1/health", headers={"Origin": GOOD_ORIGIN})
    check(f"{label} 放行来源回显 ACAO",
          headers.get("access-control-allow-origin") == [GOOD_ORIGIN],
          str(headers.get("access-control-allow-origin")))
    check(f"{label} 放行来源带凭据",
          headers.get("access-control-allow-credentials") == ["true"])
    check(f"{label} 响应带 Vary: Origin", "origin" in
          ",".join(headers.get("vary", [])).lower(), str(headers.get("vary")))

    status, raw, headers = request(base, "/api/v1/health", headers={"Origin": BAD_ORIGIN})
    check(f"{label} 未列入的来源不回显 ACAO",
          "access-control-allow-origin" not in headers,
          str(headers.get("access-control-allow-origin")))

    status, raw, headers = request(
        base, "/api/v1/me", "OPTIONS",
        headers={"Origin": GOOD_ORIGIN, "Access-Control-Request-Method": "GET",
                 "Access-Control-Request-Headers": "content-type"})
    check(f"{label} 预检 200", status == 200, f"{status}")
    check(f"{label} 预检 body 为 OK", raw == b"OK", str(raw[:20]))
    methods = (headers.get("access-control-allow-methods") or [""])[0]
    check(f"{label} 预检方法含 GET/POST", "GET" in methods and "POST" in methods, methods)
    check(f"{label} 预检允许 content-type",
          headers.get("access-control-allow-headers") == ["content-type"],
          str(headers.get("access-control-allow-headers")))
    check(f"{label} 预检带 Max-Age",
          (headers.get("access-control-max-age") or [""])[0].isdigit(),
          str(headers.get("access-control-max-age")))

    status, raw, headers = request(
        base, "/api/v1/me", "OPTIONS",
        headers={"Origin": BAD_ORIGIN, "Access-Control-Request-Method": "GET"})
    check(f"{label} 未列入来源预检 400", status == 400, f"{status}")
    check(f"{label} 未列入来源预检无 ACAO",
          "access-control-allow-origin" not in headers,
          str(headers.get("access-control-allow-origin")))

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        plain = conn.execute("SELECT count(*) FROM sessions WHERE token_hash = %s",
                             (token,)).fetchone()[0]
        hashed = conn.execute("SELECT count(*) FROM sessions WHERE token_hash = %s",
                              (hashlib.sha256(token.encode()).hexdigest(),)).fetchone()[0]
        check(f"{label} 会话令牌只存哈希", plain == 0 and hashed == 1,
              f"plain={plain} hashed={hashed}")

        stored = conn.execute(
            "SELECT password_hash FROM users WHERE email = %s", (USER[0],)).fetchone()[0]
        check(f"{label} 口令是 scrypt 哈希",
              stored.startswith("scrypt$") and USER[1] not in stored, stored[:16] + "…")

        columns = {
            f"{table}.{column}"
            for table, column in conn.execute(
                "SELECT table_name, column_name FROM information_schema.columns "
                "WHERE table_schema = 'public' AND (column_name ILIKE '%password%' "
                "OR column_name ILIKE '%secret%' OR column_name ILIKE '%token%' "
                "OR column_name ILIKE '%code%')").fetchall()
        }
        columns = {c for c in columns if not c.startswith("llm_usage.")}
        check(f"{label} 没有明文凭据列", columns == CREDENTIAL_COLUMNS,
              str(sorted(columns - CREDENTIAL_COLUMNS)))

    # 验证码同样只存哈希
    email = f"hash-check-{label.lower()}-{os.urandom(4).hex()}@bench.example.com"
    status, _, _ = request(base, "/api/v1/auth/registrations", "POST",
                           {"email": email, "password": "hash-check-123",
                            "display_name": "Hash", "locale": "zh"})
    check(f"{label} 发码请求 202", status == 202, f"{status}")
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        row = conn.execute(
            "SELECT code_hash FROM verification_codes WHERE email = %s "
            "ORDER BY created_at DESC LIMIT 1", (email,)).fetchone()
        check(f"{label} 验证码只存 64 位哈希",
              row is not None and re.fullmatch(r"[0-9a-f]{64}", row[0]) is not None,
              str(row))
        conn.execute("DELETE FROM verification_codes WHERE email = %s", (email,))
        conn.execute("DELETE FROM users WHERE email = %s", (email,))


run(GO_BASE, "Go")
run(PY_BASE, "Python")

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
