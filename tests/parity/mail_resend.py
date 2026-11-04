"""广播发信失败 → 逐条记 failed → SMTP 恢复后重发成功。

需要两个实例把 SMTP 指向一个暂时没人监听的端口（默认 127.0.0.1:2526），
脚本会在失败验证之后自己把假 SMTP 起在该端口上。

用法：
    GO_FAIL=http://127.0.0.1:18085 PY_FAIL=http://127.0.0.1:18003 \
    SMTP_PORT=2526 DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/mail_resend.py
"""

import asyncio
import http.cookiejar
import json
import os
import socket
import threading
import time
import urllib.error
import urllib.request
import uuid

import psycopg

GO_FAIL = os.environ.get("GO_FAIL", "http://127.0.0.1:18085")
PY_FAIL = os.environ.get("PY_FAIL", "http://127.0.0.1:18003")
SMTP_PORT = int(os.environ.get("SMTP_PORT", "2526"))
DB_DSN = os.environ.get("DB_DSN", "postgresql://app:app@127.0.0.1:15432/challenge")
ADMIN = ("admin@bench.example.com", os.environ.get("BENCH_PASSWORD", "bench-password-123"))

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


def login(base):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, _ = call(op, base, "/api/v1/auth/sessions",
                     {"email": ADMIN[0], "password": ADMIN[1]}, "POST")
    assert status == 200, (base, status)
    return op


class FakeSMTP:
    """最小 SMTP 服务，只统计收到的邮件数。"""

    def __init__(self, port):
        self.port = port
        self.count = 0
        self.errors = []
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
        while True:
            line = await reader.readline()
            if not line:
                break
            upper = line.decode("utf-8", "replace").strip().upper()
            if upper.startswith("EHLO"):
                writer.write(b"250-fake.local\r\n250-AUTH PLAIN\r\n250 HELP\r\n")
            elif upper.startswith("AUTH"):
                writer.write(b"235 2.7.0 Authentication successful\r\n")
            elif upper.startswith("MAIL") or upper.startswith("RCPT"):
                writer.write(b"250 2.1.0 Ok\r\n")
            elif upper == "DATA":
                writer.write(b"354 End data with <CR><LF>.<CR><LF>\r\n")
                await writer.drain()
                while True:
                    chunk = await reader.readline()
                    if not chunk or chunk in (b".\r\n", b".\n"):
                        break
                self.count += 1
                writer.write(b"250 2.0.0 Ok: queued\r\n")
            elif upper == "QUIT":
                writer.write(b"221 2.0.0 Bye\r\n")
                await writer.drain()
                break
            else:
                writer.write(b"250 2.0.0 Ok\r\n")
            await writer.drain()
        writer.close()


def port_open(port):
    with socket.socket() as sock:
        sock.settimeout(0.5)
        return sock.connect_ex(("127.0.0.1", port)) == 0


def create_and_fail(base):
    op = login(base)
    title = f"重发测试 {uuid.uuid4().hex[:8]}"
    status, created = call(op, base, "/api/v1/admin/email/broadcasts",
                           {"title": title, "body": "你好 {name}", "audience": "staff",
                            "locale": "all"}, "POST")
    assert status == 201, (base, status, created)
    broadcast_id = created["id"]
    status, _ = call(op, base, f"/api/v1/admin/email/broadcasts/{broadcast_id}/schedules",
                     {"scheduled_at": None}, "POST")
    return op, broadcast_id, status


def rows(broadcast_id):
    with psycopg.connect(DB_DSN, autocommit=True) as conn:
        return conn.execute(
            "SELECT id, status, error FROM email_messages WHERE broadcast_id = %s "
            "ORDER BY id", (broadcast_id,)).fetchall()


print(f"--- 端口 {SMTP_PORT} 初始应无人监听 ---")
check("SMTP 端口初始关闭", not port_open(SMTP_PORT))

results = {}
for label, base in (("Go", GO_FAIL), ("Python", PY_FAIL)):
    op, broadcast_id, status = create_and_fail(base)
    check(f"{label} 广播接口 200", status == 200, f"{status}")
    messages = rows(broadcast_id)
    check(f"{label} 全部记 failed", bool(messages) and all(m[1] == "failed" for m in messages),
          str([(m[1],) for m in messages]))
    check(f"{label} 失败原因已落库", bool(messages) and all(m[2] for m in messages))
    results[label] = (op, broadcast_id, [m[0] for m in messages])

print("--- 启动假 SMTP 后重发 ---")
server = FakeSMTP(SMTP_PORT)
server.start()
time.sleep(0.3)
check("SMTP 端口已就绪", port_open(SMTP_PORT))

try:
    for label, base in (("Go", GO_FAIL), ("Python", PY_FAIL)):
        op, broadcast_id, message_ids = results[label]
        before = server.count
        print(f"   {label}: 重发前 count={before}")
        status, _ = call(op, base,
                         f"/api/v1/admin/email/messages/{message_ids[0]}/resends",
                         {}, "POST")
        check(f"{label} 重发接口 200", status == 200, f"{status}")
        message = rows(broadcast_id)
        first = next((m for m in message if m[0] == message_ids[0]), None)
        check(f"{label} 状态改为 sent", first is not None and first[1] == "sent",
              str(first))
        check(f"{label} 假 SMTP 收到邮件", server.count == before + 1,
              f"{before} → {server.count}")
finally:
    server.stop()

print(f"   fake smtp errors={server.errors[:3]}")

with psycopg.connect(DB_DSN, autocommit=True) as conn:
    for _, broadcast_id, _ in results.values():
        conn.execute("DELETE FROM email_messages WHERE broadcast_id = %s", (broadcast_id,))
        conn.execute("DELETE FROM broadcasts WHERE id = %s", (broadcast_id,))

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
