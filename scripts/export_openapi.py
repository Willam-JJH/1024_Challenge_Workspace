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


def main() -> None:
    spec = app.openapi()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    tags: Counter[str] = Counter()
    for path, item in spec["paths"].items():
        for method in item:
            if method in {"get", "post", "put", "patch", "delete", "head"}:
                ops = item[method].get("tags") or ["untagged"]
                tags[ops[0]] += 1

    total = sum(tags.values())
    print(f"写入 {OUT}")
    print(f"接口总数：{total}")
    for tag, count in tags.most_common():
        print(f"  {tag:14} {count}")


if __name__ == "__main__":
    main()
