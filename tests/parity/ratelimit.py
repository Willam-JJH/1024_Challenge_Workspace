"""限流：登录失败锁定、发码冷却、验证码校验上限。

要求两点：每种情况得到预期状态码，且 Go 与 Python 完全一致。

计数在进程内，**每跑一遍都会消耗配额**：本脚本会在开始时探测计数是否已被
打满，满了就提示重启两个实例再跑（登录 IP 桶 60 次/15 分钟、发码 IP 桶
30 次/小时）。

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/ratelimit.py
"""

import http.cookiejar
import json
import os
import urllib.error
import urllib.request
import uuid

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
LOGIN_PAIR = int(os.environ.get("LOGIN_PAIR", "10"))
VERIFY_EMAIL = int(os.environ.get("VERIFY_EMAIL", "20"))

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def call(base, path, payload=None, method="GET", timeout=20):
    """返回 (状态码, 响应体, 响应头)；响应头的键统一成小写。"""
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"} if data else {}
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    try:
        with opener.open(request, timeout=timeout) as response:
            raw = response.read()
            return response.status, (json.loads(raw) if raw else {}), \
                {k.lower(): v for k, v in response.headers.items()}
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            body = json.loads(raw or b"null")
        except Exception:  # noqa: BLE001
            body = {}
        return exc.code, body, {k.lower(): v for k, v in exc.headers.items()}


def error_code(body):
    return (body or {}).get("error", {}).get("code") if isinstance(body, dict) else None


def login_attempt(base, email, password="wrong-password-123"):
    return call(base, "/api/v1/auth/sessions", {"email": email, "password": password}, "POST")


def test_login_lockout(base, label):
    probe = f"rl-probe-{uuid.uuid4().hex[:10]}@bench.example.com"
    status, body, _ = login_attempt(base, probe)
    if status == 429:
        raise SystemExit(
            f"{label} 的登录 IP 计数已耗尽，请重启该实例后重跑：{json.dumps(body)[:200]}"
        )
    check(f"{label} 计数未被历史请求打满", status == 401, f"{status}")

    email = f"rl-{uuid.uuid4().hex[:10]}@bench.example.com"
    statuses = [login_attempt(base, email)[0] for _ in range(LOGIN_PAIR)]
    check(f"{label} 前 {LOGIN_PAIR} 次失败登录都是 401",
          all(s == 401 for s in statuses), str(statuses))

    status, body, headers = login_attempt(base, email)
    check(f"{label} 第 {LOGIN_PAIR + 1} 次返回 429", status == 429, f"{status}")
    check(f"{label} 429 信封 code=rate_limited", error_code(body) == "rate_limited",
          str(error_code(body)))
    retry = headers.get("retry-after")
    check(f"{label} 带 Retry-After 头", bool(retry) and retry.isdigit(), str(retry))

    other = f"rl-other-{uuid.uuid4().hex[:10]}@bench.example.com"
    status, _, _ = login_attempt(base, other)
    check(f"{label} 换一个邮箱不受影响", status == 401, f"{status}")


def test_code_cooldown(base, label):
    email = f"rl-code-{uuid.uuid4().hex[:10]}@bench.example.com"
    payload = {"email": email, "password": "cooldown-pass-123",
               "display_name": "限流测试", "locale": "zh"}
    status, body, _ = call(base, "/api/v1/auth/registrations", payload, "POST")
    check(f"{label} 首次注册发码 202", status == 202, f"{status} {error_code(body)}")

    status, body, headers = call(base, "/api/v1/auth/registrations", payload, "POST")
    check(f"{label} 60 秒内重复发码 429", status == 429, f"{status}")
    check(f"{label} 冷却 429 信封 code=rate_limited", error_code(body) == "rate_limited",
          str(error_code(body)))
    retry = headers.get("retry-after") or "0"
    check(f"{label} 冷却 Retry-After 在 1..60", retry.isdigit() and 1 <= int(retry) <= 60, retry)

    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        conn.execute("DELETE FROM verification_codes WHERE email = %s", (email,))
        conn.execute("DELETE FROM users WHERE email = %s", (email,))


def test_verify_limit(base, label):
    email = f"rl-verify-{uuid.uuid4().hex[:10]}@bench.example.com"
    statuses = [
        call(base, "/api/v1/auth/registrations/verifications",
             {"email": email, "code": "000000"}, "POST")[0]
        for _ in range(VERIFY_EMAIL)
    ]
    check(f"{label} 前 {VERIFY_EMAIL} 次校验都是 404",
          all(s == 404 for s in statuses), str(statuses[:5]))

    status, body, headers = call(
        base, "/api/v1/auth/registrations/verifications",
        {"email": email, "code": "000000"}, "POST")
    check(f"{label} 第 {VERIFY_EMAIL + 1} 次校验 429", status == 429, f"{status}")
    check(f"{label} 校验 429 信封 code=rate_limited", error_code(body) == "rate_limited",
          str(error_code(body)))
    check(f"{label} 校验 429 带 Retry-After", bool(headers.get("retry-after")),
          str(headers.get("retry-after")))


for label, base in (("Go", GO_BASE), ("Python", PY_BASE)):
    print(f"\n=== {label} ({base}) ===")
    test_login_lockout(base, label)
    test_code_cooldown(base, label)
    test_verify_limit(base, label)

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
