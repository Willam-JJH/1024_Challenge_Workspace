"""上传滥用：文件名穿越、分片顺序、超限、下发类型。

需要两个实例把单文件上限调小，便于构造超限请求：
    APP_MAX_UPLOAD_BYTES=1048576

用法：
    GO_BASE=http://127.0.0.1:18080 PY_BASE=http://127.0.0.1:18000 \
    DB_DSN=postgresql://app:app@127.0.0.1:15432/challenge \
    python tests/parity/upload_security.py
"""

import http.cookiejar
import json
import os
import urllib.error
import urllib.request

GO_BASE = os.environ.get("GO_BASE", "http://127.0.0.1:18080")
PY_BASE = os.environ.get("PY_BASE", "http://127.0.0.1:18000")
USER = ("cw-1793711395-0@bench.example.com",
        os.environ.get("BENCH_PASSWORD", "bench-password-123"))
LIMIT = int(os.environ.get("MAX_UPLOAD_BYTES", "1048576"))

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def call(op, base, path, payload=None, method="GET", raw=None, headers=None):
    """payload 走 JSON，raw 走原始字节。"""
    if raw is not None:
        data = raw
        hdrs = dict(headers or {})
    elif payload is not None:
        data = json.dumps(payload).encode()
        hdrs = {"Content-Type": "application/json"}
    else:
        data = None
        hdrs = dict(headers or {})
    request = urllib.request.Request(base + path, data=data, method=method, headers=hdrs)
    try:
        with op.open(request, timeout=60) as response:
            body = response.read()
            return response.status, body, {k.lower(): v for k, v in response.headers.items()}
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read(), {k.lower(): v for k, v in exc.headers.items()}


def as_json(raw):
    try:
        return json.loads(raw or b"null")
    except Exception:  # noqa: BLE001
        return {}


def login(base):
    jar = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    status, body, _ = call(op, base, "/api/v1/auth/sessions",
                           {"email": USER[0], "password": USER[1]}, "POST")
    assert status == 200, (base, status, body[:200])
    return op


def create_asset(op, base, filename, content_type="application/octet-stream", size=0):
    status, body, _ = call(op, base, "/api/v1/media/assets",
                           {"filename": filename, "content_type": content_type,
                            "size": size, "purpose": "submission"}, "POST")
    return status, as_json(body)


def run(base, label):
    print(f"\n=== {label} ({base}) ===")
    op = login(base)
    assets = []

    def new_asset(filename, content_type="application/octet-stream"):
        status, body = create_asset(op, base, filename, content_type)
        assert status == 201, (label, status, body)
        assets.append(body["asset"]["id"])
        return body["asset"]

    print("-- 文件名穿越 --")
    asset = new_asset("../../etc/passwd")
    name = asset["filename"]
    check(f"{label} 文件名已去掉路径", "/" not in name and "\\" not in name and ".." not in name,
          name)
    status, body, headers = call(op, base, f"/api/v1/media/assets/{asset['id']}/content",
                                 raw=b"passwd-content", method="PUT")
    check(f"{label} 穿越文件名仍可正常上传", status == 200, f"{status}")
    status, body, headers = call(urllib.request.build_opener(), base,
                                 f"/api/v1/files/{asset['id']}")
    check(f"{label} 读回内容一致", status == 200 and body == b"passwd-content", f"{status}")

    status, _, _ = call(urllib.request.build_opener(), base,
                        "/api/v1/files/..%2F..%2Fetc%2Fpasswd")
    check(f"{label} 路径穿越读取 404", status == 404, f"{status}")

    print("-- 分片按序号合并 --")
    asset = new_asset("parts.bin")
    statuses = [
        call(op, base, f"/api/v1/media/assets/{asset['id']}/parts/{index}",
             raw=bytes([index]), method="PUT")[0]
        for index in range(1, 13)
    ]
    check(f"{label} 12 个分片都上传成功", all(s == 200 for s in statuses), str(statuses))
    status, body, _ = call(op, base, f"/api/v1/media/assets/{asset['id']}/completions",
                           {}, "POST")
    check(f"{label} 合并接口 200", status == 200, f"{status}")
    status, body, _ = call(urllib.request.build_opener(), base,
                           f"/api/v1/files/{asset['id']}")
    check(f"{label} 合并顺序为 1..12", status == 200 and body == bytes(range(1, 13)),
          str(list(body[:14])))

    print("-- 超限 --")
    asset = new_asset("big.bin")
    status, _, _ = call(op, base, f"/api/v1/media/assets/{asset['id']}/content",
                        raw=b"x" * (LIMIT + 1), method="PUT")
    check(f"{label} 单文件超限 413", status == 413, f"{status}")

    asset = new_asset("big-parts.bin")
    first = call(op, base, f"/api/v1/media/assets/{asset['id']}/parts/1",
                 raw=b"a" * (LIMIT - 1024), method="PUT")[0]
    second = call(op, base, f"/api/v1/media/assets/{asset['id']}/parts/2",
                  raw=b"b" * 2048, method="PUT")[0]
    check(f"{label} 分片单独不超限", first == 200, f"{first}")
    check(f"{label} 分片累计超限 413", second == 413, f"{second}")

    print("-- 下发类型 --")
    asset = new_asset("evil.html", "text/html")
    status, _, _ = call(op, base, f"/api/v1/media/assets/{asset['id']}/content",
                        raw=b"<html><script>alert(1)</script></html>", method="PUT")
    check(f"{label} HTML 上传成功", status == 200, f"{status}")
    status, body, headers = call(urllib.request.build_opener(), base,
                                 f"/api/v1/files/{asset['id']}")
    served = headers.get("content-type", "").split(";")[0]
    check(f"{label} 不按 text/html 下发", served != "text/html", served)
    check(f"{label} 带 nosniff", headers.get("x-content-type-options") == "nosniff",
          str(headers.get("x-content-type-options")))
    check(f"{label} 强制下载",
          headers.get("content-disposition", "").startswith("attachment"),
          headers.get("content-disposition", ""))

    for asset_id in assets:
        call(op, base, f"/api/v1/media/assets/{asset_id}", None, "DELETE")


run(GO_BASE, "Go")
run(PY_BASE, "Python")

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    raise SystemExit(1)
