"""导出 OpenAPI 规范并统计接口数量。

用法（在 backend/ 下）::

    uv run python ../scripts/export_openapi.py
"""

import json
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "backend"))

from app.main import app  # noqa: E402

OUT = ROOT / "docs" / "api" / "openapi.json"

# 只在 Go 侧实现的接口。Python 实现已进入退场流程，新能力不再两边都写；
# 这份覆盖层随导出一起合并，保证重新导出时新接口不会丢。
ADDITIONS = ROOT / "docs" / "api" / "openapi-additions.json"


def merge_additions(spec: dict) -> int:
    if not ADDITIONS.exists():
        return 0

    extra = json.loads(ADDITIONS.read_text(encoding="utf-8"))
    added = 0
    for path, item in extra.get("paths", {}).items():
        operations = spec.setdefault("paths", {}).setdefault(path, {})
        for method, operation in item.items():
            operations[method] = operation
            added += 1
    schemas = spec.setdefault("components", {}).setdefault("schemas", {})
    for name, schema in extra.get("components", {}).get("schemas", {}).items():
        # 基础规范里已有的同名 schema 以 Python 导出为准，覆盖层只补缺。
        schemas.setdefault(name, schema)
    return added


def main() -> None:
    spec = app.openapi()
    added = merge_additions(spec)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    # 固定用 LF：Windows 下 write_text 会把 \n 翻成 CRLF，和仓库里的行尾打架。
    with OUT.open("w", encoding="utf-8", newline="\n") as handle:
        handle.write(json.dumps(spec, ensure_ascii=False, indent=2) + "\n")

    tags: Counter[str] = Counter()
    for path, item in spec["paths"].items():
        for method in item:
            if method in {"get", "post", "put", "patch", "delete", "head"}:
                ops = item[method].get("tags") or ["untagged"]
                tags[ops[0]] += 1

    total = sum(tags.values())
    print(f"写入 {OUT}")
    print(f"接口总数：{total}（其中覆盖层补充 {added} 个）")
    for tag, count in tags.most_common():
        print(f"  {tag:14} {count}")


if __name__ == "__main__":
    main()
