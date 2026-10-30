"""投票与榜单的公共逻辑。"""

from datetime import UTC, datetime

from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.errors import Conflict, Forbidden, NotFound
from app.models.event import Company, Event, Membership
from app.models.vote import Vote


def live_counts(db: DbSession, event_id: str) -> dict[str, int]:
    rows = db.execute(
        select(Vote.company_id, func.count(Vote.id))
        .where(Vote.event_id == event_id, Vote.status == "live")
        .group_by(Vote.company_id)
    ).all()
    return {company_id: int(count) for company_id, count in rows}


def ranks(counts: dict[str, int]) -> dict[str, int]:
    """竞赛排名：同分同名次，下一名次跳过。"""
    ordered = sorted(counts.items(), key=lambda item: (-item[1], item[0]))
    result: dict[str, int] = {}
    previous_coins: int | None = None
    previous_rank = 0
    for index, (company_id, coins) in enumerate(ordered, start=1):
        if coins != previous_coins:
            previous_rank = index
            previous_coins = coins
        result[company_id] = previous_rank
    return result


def board(db: DbSession, event: Event) -> list[dict]:
    counts = live_counts(db, event.id)
    rank_of = ranks(counts)
    companies = db.scalars(
        select(Company).where(Company.event_id == event.id).order_by(Company.name)
    ).all()
    rows = [
        {
            "company_id": company.id,
            "name": company.name,
            "slug": company.slug,
            "coins": counts.get(company.id, 0),
            "rank": rank_of.get(company.id, len(counts) + 1),
        }
        for company in companies
    ]
    rows.sort(key=lambda row: (row["coins"] * -1, row["rank"], row["name"]))
    return rows


def my_picks(db: DbSession, event_id: str, user_id: str) -> list[Vote]:
    return list(
        db.scalars(
            select(Vote)
            .where(
                Vote.event_id == event_id, Vote.voter_id == user_id, Vote.status == "live"
            )
            .order_by(Vote.created_at)
        ).all()
    )


def _company_of_event(db: DbSession, company_id: str, event: Event) -> Company:
    company = db.get(Company, company_id)
    if company is None or company.event_id != event.id:
        raise NotFound("公司不存在")
    return company


def _assert_not_own_company(db: DbSession, company_id: str, user_id: str) -> None:
    own = db.scalar(
        select(Membership).where(
            Membership.company_id == company_id, Membership.user_id == user_id
        )
    )
    if own is not None:
        raise Forbidden("不能投给自己所在的公司")


def invest(db: DbSession, event: Event, user_id: str, company_id: str) -> Vote:
    if not event.voting_open:
        raise Forbidden("投票已关闭")
    company = _company_of_event(db, company_id, event)
    _assert_not_own_company(db, company.id, user_id)

    existing = db.scalar(
        select(Vote).where(
            Vote.event_id == event.id, Vote.voter_id == user_id, Vote.company_id == company.id
        )
    )
    if existing is not None and existing.status == "live":
        return existing

    picks = my_picks(db, event.id, user_id)
    if len(picks) >= settings.wallet_max_picks:
        raise Conflict(f"最多只能投给 {settings.wallet_max_picks} 家公司")

    if existing is not None:
        existing.status = "live"
        return existing

    vote = Vote(event_id=event.id, voter_id=user_id, company_id=company.id, status="live")
    db.add(vote)
    db.flush()
    return vote


def withdraw(db: DbSession, event: Event, user_id: str, company_id: str) -> None:
    existing = db.scalar(
        select(Vote).where(
            Vote.event_id == event.id, Vote.voter_id == user_id, Vote.company_id == company_id
        )
    )
    if existing is not None and existing.status == "live":
        existing.status = "withdrawn"


def replace_wallet(db: DbSession, event: Event, user_id: str, company_ids: list[str]) -> None:
    if not event.voting_open:
        raise Forbidden("投票已关闭")
    unique_ids = list(dict.fromkeys(company_ids))
    if len(unique_ids) > settings.wallet_max_picks:
        raise Conflict(f"最多只能投给 {settings.wallet_max_picks} 家公司")
    for company_id in unique_ids:
        _company_of_event(db, company_id, event)
        _assert_not_own_company(db, company_id, user_id)

    current = {vote.company_id: vote for vote in my_picks(db, event.id, user_id)}
    for company_id, vote in current.items():
        if company_id not in unique_ids:
            vote.status = "withdrawn"
    for company_id in unique_ids:
        if company_id not in current:
            db.add(
                Vote(event_id=event.id, voter_id=user_id, company_id=company_id, status="live")
            )


def board_payload(db: DbSession, event: Event) -> dict:
    rows = board(db, event)
    return {
        "items": rows,
        "total_coins": sum(row["coins"] for row in rows),
        "updated_at": datetime.now(UTC).isoformat(),
    }
