"""把从旧仓库提取的端点清单整理成文档。

输入：legacy-endpoints.json（由提取脚本生成，只含方法/路径/摘要/鉴权）
输出：docs/api/legacy-endpoints.md
"""

import json
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "docs" / "api" / "legacy-endpoints.json"
OUT = ROOT / "docs" / "api" / "legacy-endpoints.md"

AUTH_ZH = {"public": "公开", "authenticated": "登录", "staff": "组织者"}


def main() -> None:
    rows = json.loads(SRC.read_text(encoding="utf-8"))
    grouped: dict[str, list[dict]] = defaultdict(list)
    for row in rows:
        grouped[row["module"]].append(row)

    lines = [
        "# 旧接口清单（功能参照）",
        "",
        f"共 {len(rows)} 个端点，从原 Unscripted 仓库提取，仅作功能对照。",
        "新接口按 `docs/api/conventions.md` 的规范重新设计，路径不一一对应。",
        "",
    ]
    for module in sorted(grouped):
        lines.append(f"## {module}（{len(grouped[module])}）")
        lines.append("")
        lines.append("| 方法 | 路径 | 鉴权 | 摘要 |")
        lines.append("|---|---|---|---|")
        for row in sorted(grouped[module], key=lambda r: (r["path"], r["method"])):
            summary = row["summary"].replace("|", "\\|")
            lines.append(
                f"| {row['method']} | `{row['path']}` | {AUTH_ZH.get(row['auth'], row['auth'])} | {summary} |"
            )
        lines.append("")

    OUT.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"写入 {OUT}（{len(rows)} 个端点，{len(grouped)} 个模块）")


if __name__ == "__main__":
    main()
