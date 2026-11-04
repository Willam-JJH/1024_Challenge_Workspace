"""备份与恢复演练：pg_dump 一个库，恢复到临时库，再逐表比对行数。

只在本地基准容器上跑，不会碰生产。默认：
    CONTAINER=ubench-pg SRC=challenge DST=challenge_restore

dump 走 stdout 落到本机临时文件，restore 从 stdin 灌回容器，
避免 Windows / Git Bash 的路径转换问题。
"""

import os
import shlex
import subprocess
import sys
import tempfile

CONTAINER = os.environ.get("CONTAINER", "ubench-pg")
SRC = os.environ.get("SRC", "challenge")
DST = os.environ.get("DST", "challenge_restore")
DBUSER = os.environ.get("DBUSER", "app")
TABLES = ["users", "companies", "memberships", "projects", "votes", "media_assets"]

checks = []


def check(name, ok, extra=""):
    checks.append(bool(ok))
    print(("PASS " if ok else "FAIL ") + name + (f"  {extra}" if extra else ""))


def run(command, check_code=True):
    result = subprocess.run(["docker", "exec", CONTAINER, *shlex.split(command)],
                            capture_output=True, text=True)
    if check_code and result.returncode != 0:
        raise SystemExit(f"命令失败: {command}\n{result.stderr}")
    return result.stdout.strip()


def scalar(sql, database):
    return int(run(f'psql -U {DBUSER} -d {database} -tAc "{sql}"') or 0)


def tables(database):
    return scalar(
        "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'",
        database)


dump_path = os.path.join(tempfile.gettempdir(), "parity_restore.dump")
print(f"--- dump {SRC} -> {dump_path} ---")
with open(dump_path, "wb") as handle:
    result = subprocess.run(["docker", "exec", CONTAINER, "pg_dump", "-U", DBUSER,
                             "-d", SRC, "-Fc"], stdout=handle, stderr=subprocess.PIPE)
if result.returncode != 0:
    raise SystemExit(f"pg_dump 失败: {result.stderr.decode(errors='replace')}")
size = os.path.getsize(dump_path)
check("dump 非空", size > 0, f"{size} bytes")

run(f"dropdb -U {DBUSER} --if-exists {DST}")
run(f"createdb -U {DBUSER} {DST}")
print(f"--- restore 到 {DST} ---")
with open(dump_path, "rb") as handle:
    result = subprocess.run(["docker", "exec", "-i", CONTAINER, "pg_restore",
                             "-U", DBUSER, "-d", DST, "--no-owner"],
                            stdin=handle, capture_output=True)
if result.returncode != 0:
    raise SystemExit(f"pg_restore 失败: {result.stderr.decode(errors='replace')[:400]}")

check("表数量一致", tables(SRC) == tables(DST), f"{tables(SRC)} vs {tables(DST)}")
for table in TABLES:
    source = scalar(f"SELECT count(*) FROM {table}", SRC)
    target = scalar(f"SELECT count(*) FROM {table}", DST)
    check(f"{table} 行数一致", source == target, f"{source} vs {target}")

def has_alembic(database):
    return scalar("SELECT count(*) FROM information_schema.tables "
                  "WHERE table_schema = 'public' AND table_name = 'alembic_version'",
                  database)


if has_alembic(SRC) and has_alembic(DST):
    version_src = run(
        f"psql -U {DBUSER} -d {SRC} -tAc 'SELECT version_num FROM alembic_version'")
    version_dst = run(
        f"psql -U {DBUSER} -d {DST} -tAc 'SELECT version_num FROM alembic_version'")
    check("alembic 版本一致", version_src == version_dst, f"{version_src} vs {version_dst}")
else:
    print(f"   （{SRC} 没有 alembic_version 表，跳过版本比对）")

run(f"dropdb -U {DBUSER} --if-exists {DST}")
os.remove(dump_path)
print("cleanup: 临时库与 dump 已删除")

print(f"\n{sum(checks)}/{len(checks)} checks passed")
if not all(checks):
    sys.exit(1)
