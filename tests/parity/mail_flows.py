"""邮件业务全链路：注册验证码、口令重置、邀请激活、广播，全部走真实 SMTP 协议。

脚本会自己起一个假 SMTP（默认 127.0.0.1:2527），两个实例要指向它：
    APP_MAIL_DRIVER=smtp APP_SMTP_HOST=127.0.0.1 APP_SMTP_PORT=2527 \
    APP_SMTP_TLS=none APP_SMTP_USER=fake APP_SMTP_PASSWORD=fake \
    APP_MAIL_FROM=no-reply@bench.example.com

注册后马上要走口令重置，同一邮箱 60 秒内只能发一次码，所以还要：
    APP_RATE_LIMIT_CODE_COOLDOWN=0

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/mail_flows.py
"""

import asyncio
import email
import http.cookiejar
import json
import os
import re
import threading
import time
import urllib.error
import urllib.request
import uuid
from email.header import decode_header, make_header

import psycopg

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
SMTP_PORT = int(os.environ.get("SMTP_PORT", "2527"))
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
ADMIN = ("admin@bench.example.com", os.environ.get("BENCH_PASSWORD", "bench-password-123"))
CAPTAIN = ("cw-1793711395-1@bench.example.com", os.environ.get("BENCH_PASSWORD",
                                                               "bench-password-123"))
COMPANY_ID = os.environ.get("COMPANY_ID", "3199aad6835e9cfb6a6996d4aeffaadb")

checks = []
cleanup: list[tuple[str, str]] = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


class FakeSMTP:
    """最小 SMTP 服务：记录收件人、主题与解码后的正文。"""

    def __init__(self, port):
        self.port = port
        self.messages: list[dict] = []
        self.errors: list[str] = []
        self.ready = threading.Event()
        self._loop = None
        self._stop = None

    def start(self):
        threading.Thread(target=self._run, daemon=True).start()
        if not self.ready.wait(5):
            raise RuntimeError("fake smtp 启动超时")

    def stop(self):
        if self._loop is not None and self._stop is not None:
            self._loop.call_soon_threadsafe(self._stop.set)

    def _run(self):
        self._loop = asyncio.new_event_loop()
        asyncio.set_event_loop(self._loop)
        self._loop.run_until_complete(self._main())

    async def _main(self):
        self._stop = asyncio.Event()
        server = await asyncio.start_server(self._handle, "127.0.0.1", self.port)
        self.ready.set()
        async with server:
            await self._stop.wait()

    async def _handle(self, reader, writer):
        try:
            await self._session(reader, writer)
        except Exception as exc:  # noqa: BLE001
            self.errors.append(f"{type(exc).__name__}: {exc}")
        finally:
            writer.close()

    async def _session(self, reader, writer):
        writer.write(b"220 fake.local ESMTP\r\n")
        await writer.drain()
        sender, recipients, data = "", [], b""
        while True:
            line = await reader.readline()
            if not line:
                break
            text = line.decode("utf-8", "replace").strip()
            upper = text.upper()
            if upper.startswith("EHLO"):
                writer.write(b"250-fake.local\r\n250-AUTH PLAIN\r\n250 HELP\r\n")
            elif upper.startswith("AUTH"):
                writer.write(b"235 2.7.0 Authentication successful\r\n")
            elif upper.startswith("MAIL FROM:"):
                sender = text[10:].strip(" <>")
                writer.write(b"250 2.1.0 Ok\r\n")
            elif upper.startswith("RCPT TO:"):
                recipients.append(text[8:].strip(" <>"))
                writer.write(b"250 2.1.0 Ok\r\n")
            elif upper == "DATA":
                writer.write(b"354 End data with <CR><LF>.<CR><LF>\r\n")
                buffer = b""
                while True:
                    chunk = await reader.readline()
                    if not chunk or chunk in (b".\r\n", b".\n"):
                        break
                    buffer += chunk
                data = buffer
                self._store(sender, recipients, data)
                writer.write(b"250 2.0.0 Ok: queued\r\n")
                sender, recipients, data = "", [], b""
            elif upper == "QUIT":
                writer.write(b"221 2.0.0 Bye\r\n")
                await writer.drain()
                break
            elif upper == "RSET":
                sender, recipients = "", []
                writer.write(b"250 2.0.0 Ok\r\n")
            else:
                writer.write(b"250 2.0.0 Ok\r\n")
            await writer.drain()
        writer.close()

    def _store(self, sender, recipients, raw):
        subject, body = parse_message(raw)
        for recipient in recipients:
            self.messages.append({"from": sender, "to": recipient,
                                  "subject": subject, "body": body})

    def wait_for(self, recipient, contains="", timeout=15.0):
        deadline = time.time() + timeout
        while time.time() < deadline:
            for message in reversed(self.messages):
                if message["to"] == recipient and contains in message["subject"]:
                    return message
            time.sleep(0.1)
        return None


def parse_message(raw: bytes):
    try:
        msg = email.message_from_bytes(raw)
        subject = str(make_header(decode_header(msg.get("Subject", ""))))
        if msg.is_multipart():
            chunks = []
            for part in msg.walk():
                payload = part.get_payload(decode=True)
                if payload:
                    chunks.append(payload.decode("utf-8", "replace"))
            body = "\n".join(chunks)
        else:
            payload = msg.get_payload(decode=True)
            body = payload.decode("utf-8", "replace") if payload else str(msg.get_payload())
        return subject, body
    except Exception:  # noqa: BLE001
        text = raw.decode("utf-8", "replace")
        match = re.search(r"Subject: (.*)", text)
        return (match.group(1) if match else ""), text


def call(op, base, path, payload=None, method="GET"):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"} if data else {}
    request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
    try:
        with op.open(request, timeout=30) as response:
            body = response.read()
            return response.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as exc:
        body = exc.read()
        try:
            return exc.code, json.loads(body or b"null")
        except Exception:  # noqa: BLE001
            return exc.code, {}


def login(base, account):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, body = call(op, base, "/api/v1/auth/sessions",
                        {"email": account[0], "password": account[1]}, "POST")
    assert status == 200, (base, account[0], status, body)
    return op


def code_of(message):
    match = re.search(r"\b(\d{6})\b", message["body"])
    return match.group(1) if match else None


def token_of(message):
    match = re.search(r"activations/([A-Za-z0-9_-]{16,})", message["body"])
    return match.group(1) if match else None


def run(base, label, smtp: FakeSMTP):
    print(f"\n=== {label} ({base}) ===")

    print("-- 注册验证码 --")
    email_addr = f"mail-reg-{label.lower()}-{uuid.uuid4().hex[:8]}@bench.example.com"
    cleanup.append(("user", email_addr))
    status, _ = call(urllib.request.build_opener(), base, "/api/v1/auth/registrations",
                     {"email": email_addr, "password": "mail-flow-pass-123",
                      "display_name": "邮件流程", "locale": "zh"}, "POST")
    check(f"{label} 注册接口 202", status == 202, f"{status}")
    message = smtp.wait_for(email_addr, "验证码")
    check(f"{label} 收到注册验证码邮件", message is not None,
          message["subject"] if message else "超时")
    code = code_of(message) if message else None
    check(f"{label} 邮件里有 6 位验证码", code is not None, str(code))
    status, body = call(urllib.request.build_opener(), base,
                        "/api/v1/auth/registrations/verifications",
                        {"email": email_addr, "code": code or "000000"}, "POST")
    check(f"{label} 验证码激活并登录 200", status == 200, f"{status}")

    print("-- 口令重置 --")
    smtp.messages.clear()
    status, _ = call(urllib.request.build_opener(), base,
                     "/api/v1/auth/password-reset-requests", {"email": email_addr}, "POST")
    check(f"{label} 申请重置 200", status == 200, f"{status}")
    message = smtp.wait_for(email_addr, "重置")
    check(f"{label} 收到重置验证码邮件", message is not None,
          message["subject"] if message else "超时")
    code = code_of(message) if message else None
    status, body = call(urllib.request.build_opener(), base,
                        "/api/v1/auth/password-reset-grants",
                        {"email": email_addr, "code": code or "000000"}, "POST")
    check(f"{label} 验证码换授权 200", status == 200, f"{status}")
    grant = body.get("token") if isinstance(body, dict) else None
    status, _ = call(urllib.request.build_opener(), base, "/api/v1/auth/password-resets",
                     {"token": grant or "", "new_password": "mail-flow-new-456"}, "POST")
    check(f"{label} 用授权设新口令 200", status == 200, f"{status}")
    op = urllib.request.build_opener()
    status, _ = call(op, base, "/api/v1/auth/sessions",
                     {"email": email_addr, "password": "mail-flow-new-456"}, "POST")
    check(f"{label} 新口令可登录", status == 200, f"{status}")

    print("-- 邀请激活 --")
    invitee = f"mail-inv-{label.lower()}-{uuid.uuid4().hex[:8]}@bench.example.com"
    cleanup.append(("user", invitee))
    captain = login(base, CAPTAIN)
    smtp.messages.clear()
    status, body = call(captain, base, f"/api/v1/companies/{COMPANY_ID}/invitations",
                        {"email": invitee}, "POST")
    check(f"{label} 发出邀请 201", status == 201, f"{status} {body}")
    message = smtp.wait_for(invitee)
    check(f"{label} 收到邀请邮件", message is not None,
          message["subject"] if message else "超时")
    token = token_of(message) if message else None
    check(f"{label} 邮件里有激活令牌", token is not None, str(token))
    status, body = call(urllib.request.build_opener(), base,
                        f"/api/v1/auth/activations/{token}", None, "GET")
    check(f"{label} 预览激活信息 200", status == 200, f"{status}")
    check(f"{label} 预览邮箱正确", isinstance(body, dict) and body.get("email") == invitee,
          str(body))
    status, _ = call(urllib.request.build_opener(), base, "/api/v1/auth/activations",
                     {"token": token or "", "password": "mail-flow-invite-123"}, "POST")
    check(f"{label} 激活并入队 200", status == 200, f"{status}")

    print("-- 广播 --")
    admin = login(base, ADMIN)
    smtp.messages.clear()
    status, body = call(admin, base, "/api/v1/admin/email/broadcasts",
                        {"title": f"邮件流程 {label}", "body": "你好 {name}",
                         "audience": "staff", "locale": "all"}, "POST")
    check(f"{label} 建广播 201", status == 201, f"{status}")
    broadcast_id = body.get("id") if isinstance(body, dict) else None
    if broadcast_id:
        cleanup.append(("broadcast", broadcast_id))
        status, _ = call(admin, base,
                         f"/api/v1/admin/email/broadcasts/{broadcast_id}/schedules",
                         {"scheduled_at": None}, "POST")
        check(f"{label} 立即发送 200", status == 200, f"{status}")
        time.sleep(0.5)
        sent = [m for m in smtp.messages if "邮件流程" in m["subject"]]
        check(f"{label} 收件人收到广播", len(sent) >= 2, f"{len(sent)} 封")


server = FakeSMTP(SMTP_PORT)
server.start()
try:
    run(GO_BASE, "Go", server)
    run(PY_BASE, "Python", server)
finally:
    server.stop()
    print(f"   fake smtp errors={server.errors[:3]}")

with psycopg.connect(DB_DSN, autocommit=True) as conn:
    for kind, value in cleanup:
        if kind == "user":
            conn.execute("DELETE FROM memberships WHERE user_id = "
                         "(SELECT id FROM users WHERE email = %s)", (value,))
            conn.execute("DELETE FROM invitations WHERE email = %s", (value,))
            conn.execute("DELETE FROM verification_codes WHERE email = %s", (value,))
            conn.execute("DELETE FROM sessions WHERE user_id = "
                         "(SELECT id FROM users WHERE email = %s)", (value,))
            conn.execute("DELETE FROM users WHERE email = %s", (value,))
        else:
            conn.execute("DELETE FROM email_messages WHERE broadcast_id = %s", (value,))
            conn.execute("DELETE FROM broadcasts WHERE id = %s", (value,))

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
