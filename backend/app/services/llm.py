"""LLM 用量与预算。"""

from datetime import UTC, datetime, timedelta

from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.models.event import Company, Membership
from app.models.identity import User
from app.models.llm import LlmUsage


def spent(db: DbSession) -> int:
    return int(db.scalar(select(func.coalesce(func.sum(LlmUsage.cost_micro), 0))) or 0)


def requests_count(db: DbSession) -> int:
    return int(db.scalar(select(func.count()).select_from(LlmUsage)) or 0)


def burn_24h(db: DbSession) -> int:
    since = datetime.now(UTC) - timedelta(hours=24)
    return int(
        db.scalar(
            select(func.coalesce(func.sum(LlmUsage.cost_micro), 0)).where(
                LlmUsage.created_at >= since
            )
        )
        or 0
    )


def usage_by_user(db: DbSession) -> list[dict]:
    rows = db.execute(
        select(
            User.display_name,
            LlmUsage.prompt_tokens,
            LlmUsage.completion_tokens,
            LlmUsage.cost_micro,
        ).join(User, User.id == LlmUsage.user_id)
    ).all()
    grouped: dict[str, dict] = {}
    for name, prompt, completion, cost in rows:
        item = grouped.setdefault(
            name, {"label": name, "prompt_tokens": 0, "completion_tokens": 0, "cost_micro": 0}
        )
        item["prompt_tokens"] += prompt
        item["completion_tokens"] += completion
        item["cost_micro"] += cost
    return sorted(grouped.values(), key=lambda row: row["cost_micro"], reverse=True)


def usage_by_team(db: DbSession) -> list[dict]:
    rows = db.execute(
        select(
            Company.name,
            LlmUsage.prompt_tokens,
            LlmUsage.completion_tokens,
            LlmUsage.cost_micro,
        )
        .join(Membership, Membership.user_id == LlmUsage.user_id)
        .join(Company, Company.id == Membership.company_id)
    ).all()
    grouped: dict[str, dict] = {}
    for name, prompt, completion, cost in rows:
        item = grouped.setdefault(
            name, {"label": name, "prompt_tokens": 0, "completion_tokens": 0, "cost_micro": 0}
        )
        item["prompt_tokens"] += prompt
        item["completion_tokens"] += completion
        item["cost_micro"] += cost
    return sorted(grouped.values(), key=lambda row: row["cost_micro"], reverse=True)
