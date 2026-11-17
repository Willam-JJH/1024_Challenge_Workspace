"""把 Alembic 的迁移链编译成 Go 侧可直接执行的分版本 SQL。

用法（在仓库根目录）::

    backend/.venv/Scripts/python.exe scripts/export_schema_sql.py
    backend/.venv/Scripts/python.exe scripts/export_schema_sql.py --check

输出到 ``backend-go/internal/migrate/sql/NNNN_<revision>_<slug>.sql``，由 Go 侧
``./api migrate`` 按文件名顺序执行。**schema 的唯一来源仍然是 backend/migrations**：
这份脚本只是把 Python 的迁移编译成部署产物，改表依旧只写 Alembic 版本。

``--check`` 只比对不写盘，发现不一致时退出码为 1，适合放进 CI。
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BACKEND = ROOT / "backend"
OUT_DIR = ROOT / "backend-go" / "internal" / "migrate" / "sql"

# 离线模式仍然要一个方言；这里只用来生成 PostgreSQL 的 DDL，不会真正连接。
OFFLINE_URL = "postgresql+psycopg://schema-export:schema-export@127.0.0.1:5432/schema-export"

WRAPPER_LINE = re.compile(r"^\s*(BEGIN|COMMIT)\s*;\s*$", re.IGNORECASE)


def revision_chain() -> list:
    """按 base → head 的顺序返回迁移版本。有分支时直接报错。"""
    sys.path.insert(0, str(BACKEND))
    from alembic.config import Config
    from alembic.script import ScriptDirectory

    cwd = os.getcwd()
    os.chdir(BACKEND)
    try:
        script = ScriptDirectory.from_config(Config(str(BACKEND / "alembic.ini")))
        revisions = list(script.walk_revisions())
    finally:
        os.chdir(cwd)
    revisions.reverse()
    for index, revision in enumerate(revisions):
        if isinstance(revision.down_revision, tuple):
            raise SystemExit(
                f"迁移 {revision.revision} 有多个父版本，Go 迁移器只支持线性链"
            )
        want = "base" if index == 0 else revisions[index - 1].revision
        if (revision.down_revision or "base") != want:
            raise SystemExit(
                f"迁移链断开：{revision.revision} 的父版本是 {revision.down_revision}，"
                f"按顺序应当是 {want}"
            )
    return revisions


def slugify(message: str | None) -> str:
    if not message:
        return "migration"
    first_line = message.strip().splitlines()[0]
    slug = re.sub(r"[^a-z0-9]+", "_", first_line.lower()).strip("_")
    return (slug or "migration")[:48].rstrip("_")


def render(revision) -> str:
    """生成单个版本的 SQL。"""
    parent = "base" if revision.down_revision is None else revision.down_revision
    env = os.environ.copy()
    env["APP_DATABASE_URL"] = OFFLINE_URL
    proc = subprocess.run(
        [sys.executable, "-m", "alembic", "upgrade", f"{parent}:{revision.revision}", "--sql"],
        cwd=BACKEND,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise SystemExit(f"生成 {revision.revision} 失败：\n{proc.stderr}")

    lines = [line for line in proc.stdout.splitlines() if not WRAPPER_LINE.match(line)]
    sql = "\n".join(lines).strip()
    if not sql:
        raise SystemExit(f"迁移 {revision.revision} 生成结果为空")
    marker = re.compile(rf"^-- Running upgrade .*-> {re.escape(revision.revision)}\b", re.MULTILINE)
    if not marker.search(sql):
        raise SystemExit(f"迁移 {revision.revision} 的输出里没有升级标记，拒绝写入")
    return sql + "\n"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="只比对，不写盘")
    args = parser.parse_args()

    revisions = revision_chain()
    wanted: dict[str, str] = {}
    for index, revision in enumerate(revisions, start=1):
        name = f"{index:04d}_{revision.revision}_{slugify(revision.doc)}.sql"
        wanted[name] = render(revision)

    stale = []
    if OUT_DIR.exists():
        stale = sorted(
            path.name for path in OUT_DIR.glob("*.sql") if path.name not in wanted
        )

    changed = []
    for name, sql in wanted.items():
        path = OUT_DIR / name
        if not path.exists() or path.read_text(encoding="utf-8") != sql:
            changed.append(name)

    if args.check:
        if changed or stale:
            for name in changed:
                print(f"需要更新：{name}")
            for name in stale:
                print(f"应当删除：{name}")
            raise SystemExit("Go 侧 SQL 与 Alembic 迁移链不一致，请重新导出")
        print(f"一致：{len(wanted)} 个版本")
        return

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for name, sql in wanted.items():
        (OUT_DIR / name).write_text(sql, encoding="utf-8", newline="\n")
    for name in stale:
        (OUT_DIR / name).unlink()
    for name in changed:
        print(f"已写入：{name}")
    if stale:
        print(f"已删除：{', '.join(stale)}")
    print(f"共 {len(wanted)} 个版本，输出目录 {OUT_DIR}")


if __name__ == "__main__":
    main()
