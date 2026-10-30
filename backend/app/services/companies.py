"""公司相关的公共查询与权限判断。"""

import re

from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.errors import Conflict, Forbidden, NotFound
from app.models.event import Company, Event, Invitation, JoinRequest, Membership
from app.models.identity import User

SLUG_RE = re.compile(r"[^a-z0-9]+")


def slugify(name: str) -> str:
    slug = SLUG_RE.sub("-", name.strip().lower()).strip("-")
    return slug[:60] or "company"


def current_event(db: DbSession) -> Event:
    event = db.scalar(select(Event).where(Event.slug == settings.current_event_slug))
    if event is None:
        raise NotFound("当前赛事尚未配置")
    return event


def company_by_ref(db: DbSession, ref: str) -> Company:
    """按 id 或 slug 查公司。"""
    company = db.get(Company, ref)
    if company is None:
        company = db.scalar(select(Company).where(Company.slug == ref))
    if company is None:
        raise NotFound("公司不存在")
    return company


def membership_of(db: DbSession, company_id: str, user_id: str) -> Membership | None:
    return db.scalar(
        select(Membership).where(
            Membership.company_id == company_id, Membership.user_id == user_id
        )
    )


def my_membership(db: DbSession, user_id: str) -> Membership | None:
    """当前赛事里我的成员关系（一人只能在一家公司）。"""
    event = current_event(db)
    return db.scalar(
        select(Membership)
        .join(Company, Company.id == Membership.company_id)
        .where(Membership.user_id == user_id, Company.event_id == event.id)
    )


def require_member(db: DbSession, company_id: str, user_id: str) -> Membership:
    row = membership_of(db, company_id, user_id)
    if row is None:
        raise Forbidden("不是该公司成员")
    return row


def require_captain(db: DbSession, company_id: str, user_id: str) -> Membership:
    row = require_member(db, company_id, user_id)
    if row.role != "captain":
        raise Forbidden("需要队长权限")
    return row


def seats_taken(db: DbSession, company_id: str) -> int:
    return int(
        db.scalar(
            select(func.count()).select_from(Membership).where(Membership.company_id == company_id)
        )
        or 0
    )


def seats_left(db: DbSession, company: Company, event: Event) -> int:
    return max(event.company_seats - seats_taken(db, company.id), 0)


def add_member(db: DbSession, company: Company, user: User, *, role: str = "member") -> Membership:
    existing = my_membership(db, user.id)
    if existing is not None:
        raise Conflict("该账号已在其他公司中")
    event = db.get(Event, company.event_id)
    if event is not None and seats_taken(db, company.id) >= event.company_seats:
        raise Conflict("公司人数已满")
    row = Membership(company_id=company.id, user_id=user.id, role=role)
    db.add(row)
    if user.role == "participant":
        user.role = "member"
    db.flush()
    return row


def pending_join_request(db: DbSession, company_id: str, user_id: str) -> JoinRequest | None:
    return db.scalar(
        select(JoinRequest).where(
            JoinRequest.company_id == company_id,
            JoinRequest.user_id == user_id,
            JoinRequest.status == "pending",
        )
    )


def pending_invitation(db: DbSession, company_id: str, email: str) -> Invitation | None:
    return db.scalar(
        select(Invitation).where(
            Invitation.company_id == company_id,
            Invitation.email == email.lower(),
            Invitation.status == "pending",
        )
    )
