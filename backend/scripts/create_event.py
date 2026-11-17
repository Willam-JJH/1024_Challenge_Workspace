"""创建或更新赛事记录（首次部署用）。

正式换届请在后台调 ``POST /api/v1/admin/events``（见 docs/design/season-lifecycle.md）；
这个脚本保留给空库部署和本机调试，可以直接写库，不受报名开关等业务约束：

用法（容器内）::

    docker compose -f infra/docker-compose.prod.yml run --rm api \
        python scripts/create_event.py --slug 1024-challenge --name "1024 Challenge"

只覆盖显式给出的字段；已存在的赛事会被更新而不是重建，可以重复执行。
"""

import argparse
import sys
from datetime import UTC, datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from sqlalchemy import select  # noqa: E402

from app.core.db import SessionLocal  # noqa: E402
from app.models.event import Event  # noqa: E402

STAGES = ("draft", "registration", "build", "judging", "results", "archived")


def parse_time(value: str) -> datetime:
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=UTC)
    return parsed.astimezone(UTC)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--slug", default="1024-challenge")
    parser.add_argument("--name")
    parser.add_argument("--stage", choices=STAGES)
    parser.add_argument("--starts-at", help="ISO 8601，例如 2026-10-15T09:00:00+08:00")
    parser.add_argument("--ends-at", help="ISO 8601")
    parser.add_argument(
        "--registration-open", action=argparse.BooleanOptionalAction, default=None
    )
    parser.add_argument("--voting-open", action=argparse.BooleanOptionalAction, default=None)
    parser.add_argument("--votes-visible", action=argparse.BooleanOptionalAction, default=None)
    parser.add_argument("--company-seats", type=int)
    parser.add_argument("--llm-budget-micro", type=int)
    parser.add_argument("--pitch-title")
    parser.add_argument("--pitch-on-air", action=argparse.BooleanOptionalAction, default=None)
    args = parser.parse_args()

    updates = {
        "name": args.name,
        "stage": args.stage,
        "starts_at": parse_time(args.starts_at) if args.starts_at else None,
        "ends_at": parse_time(args.ends_at) if args.ends_at else None,
        "registration_open": args.registration_open,
        "voting_open": args.voting_open,
        "votes_visible": args.votes_visible,
        "company_seats": args.company_seats,
        "llm_budget_micro": args.llm_budget_micro,
        "pitch_title": args.pitch_title,
        "pitch_on_air": args.pitch_on_air,
    }
    updates = {key: value for key, value in updates.items() if value is not None}

    with SessionLocal() as db:
        event = db.scalar(select(Event).where(Event.slug == args.slug))
        if event is None:
            event = Event(slug=args.slug, name=args.name or "1024 Challenge")
            for key, value in updates.items():
                setattr(event, key, value)
            db.add(event)
            db.commit()
            print(f"已创建赛事 {args.slug}（stage={event.stage}）")
            return

        for key, value in updates.items():
            setattr(event, key, value)
        db.commit()
        changed = ", ".join(sorted(updates)) or "无"
        print(f"已更新赛事 {args.slug}（字段：{changed}）")


if __name__ == "__main__":
    main()
