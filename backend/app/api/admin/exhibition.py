"""管理台：展览（介绍任务、评论审核、广告、合作与签到）。"""

import random
from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.event import Company
from app.models.exhibition import (
    Collaboration,
    Comment,
    CommentReport,
    Introduction,
    Promotion,
    Visit,
)
from app.models.identity import User
from app.schemas.common import OkOut
from app.schemas.exhibition import (
    AdminCommentOut,
    AssignmentOut,
    AssignTargetIn,
    CollaborationOut,
    ExhibitionOverviewOut,
    GivenIn,
    ModerationFlagIn,
    PresenceIn,
    PromotionOut,
)
from app.services import audit
from app.services import companies as company_service

router = APIRouter(prefix="/admin/exhibition", tags=["admin"])


def _name(db: DbSession, company_id: str) -> str:
    company = db.get(Company, company_id)
    return company.name if company else ""


def _assignment(db: DbSession, row: Introduction) -> AssignmentOut:
    return AssignmentOut(
        id=row.id,
        introducer_company=_name(db, row.introducer_company_id),
        target_company=_name(db, row.target_company_id),
        status=row.status,
        given_at=row.given_at,
        has_text=bool(row.text),
        has_reply=bool(row.reply),
    )


def _intro(db: DbSession, introduction_id: str) -> Introduction:
    row = db.get(Introduction, introduction_id)
    if row is None:
        raise NotFound("介绍任务不存在")
    return row


def _count(db: DbSession, model, *where) -> int:  # noqa: ANN001
    stmt = select(func.count()).select_from(model)
    if where:
        stmt = stmt.where(*where)
    return int(db.scalar(stmt) or 0)


@router.get("", response_model=ExhibitionOverviewOut, summary="展览总览")
def overview(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> ExhibitionOverviewOut:
    event = company_service.current_event(db)
    return ExhibitionOverviewOut(
        companies=_count(db, Company, Company.event_id == event.id),
        published_introductions=_count(
            db, Introduction, Introduction.event_id == event.id, Introduction.status == "published"
        ),
        draft_introductions=_count(
            db, Introduction, Introduction.event_id == event.id, Introduction.status == "draft"
        ),
        comments=_count(db, Comment),
        reported_comments=int(
            db.scalar(select(func.count(func.distinct(CommentReport.comment_id)))) or 0
        ),
        visits=_count(db, Visit, Visit.event_id == event.id),
        live_promotions=_count(db, Promotion, Promotion.status == "live"),
    )


# --- 介绍任务 ---------------------------------------------------------------


@router.get("/assignments", response_model=list[AssignmentOut], summary="介绍任务列表")
def list_assignments(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[AssignmentOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Introduction)
        .where(Introduction.event_id == event.id)
        .order_by(Introduction.created_at)
    ).all()
    return [_assignment(db, row) for row in rows]


@router.patch(
    "/assignments/{assignment_id}", response_model=AssignmentOut, summary="调整草稿分配"
)
def patch_assignment(
    assignment_id: str,
    payload: AssignTargetIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> AssignmentOut:
    row = _intro(db, assignment_id)
    if row.status != "draft":
        raise Conflict("只能调整草稿")
    if db.get(Company, payload.target_company_id) is None:
        raise NotFound("目标公司不存在")
    row.target_company_id = payload.target_company_id
    db.flush()
    return _assignment(db, row)


@router.post(
    "/assignments/{assignment_id}/cancellations", response_model=OkOut, summary="取消分配"
)
def cancel_assignment(
    assignment_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _intro(db, assignment_id).status = "cancelled"
    return OkOut()


@router.post(
    "/assignments/{assignment_id}/given", response_model=OkOut, summary="标记介绍已完成或撤销"
)
def mark_given(
    assignment_id: str,
    payload: GivenIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _intro(db, assignment_id)
    row.given_at = datetime.now(UTC) if payload.given else None
    return OkOut()


@router.post(
    "/assignments/{assignment_id}/reassignments",
    response_model=AssignmentOut,
    summary="换一家公司介绍",
)
def reassign(
    assignment_id: str,
    payload: AssignTargetIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> AssignmentOut:
    row = _intro(db, assignment_id)
    if db.get(Company, payload.target_company_id) is None:
        raise NotFound("目标公司不存在")
    row.target_company_id = payload.target_company_id
    row.text = None
    row.reply = None
    row.status = "draft"
    db.flush()
    return _assignment(db, row)


def _companies(db: DbSession, event_id: str) -> list[Company]:
    return list(
        db.scalars(
            select(Company).where(Company.event_id == event_id).order_by(Company.created_at)
        )
    )


@router.post(
    "/assignments/remaining", response_model=list[AssignmentOut], summary="为后来加入的公司补分配"
)
def assign_remaining(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[AssignmentOut]:
    event = company_service.current_event(db)
    companies = _companies(db, event.id)
    covered = {
        row.introducer_company_id
        for row in db.scalars(
            select(Introduction).where(
                Introduction.event_id == event.id, Introduction.status != "cancelled"
            )
        ).all()
    }
    created = []
    for company in companies:
        if company.id in covered:
            continue
        others = [other for other in companies if other.id != company.id]
        if not others:
            break
        row = Introduction(
            event_id=event.id,
            introducer_company_id=company.id,
            target_company_id=random.choice(others).id,
            status="draft",
        )
        db.add(row)
        created.append(row)
    db.flush()
    return [_assignment(db, row) for row in created]


@router.post(
    "/assignments/generation", response_model=list[AssignmentOut], summary="随机错位抽取（草稿）"
)
def generate(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[AssignmentOut]:
    event = company_service.current_event(db)
    companies = _companies(db, event.id)
    if len(companies) < 2:
        raise Conflict("至少需要两家公司")
    for row in db.scalars(select(Introduction).where(Introduction.event_id == event.id)).all():
        db.delete(row)
    db.flush()

    order = companies[:]
    random.shuffle(order)
    created = []
    for index, company in enumerate(order):
        row = Introduction(
            event_id=event.id,
            introducer_company_id=company.id,
            target_company_id=order[(index + 1) % len(order)].id,
            status="draft",
        )
        db.add(row)
        created.append(row)
    db.flush()
    return [_assignment(db, row) for row in created]


@router.post("/assignments/publications", response_model=OkOut, summary="发布全部草稿")
def publish_assignments(
    principal: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> OkOut:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Introduction).where(
            Introduction.event_id == event.id, Introduction.status == "draft"
        )
    ).all()
    if not rows:
        raise Conflict("没有待发布的草稿")
    for row in rows:
        row.status = "published"
    audit.record(
        db,
        actor_id=principal.user.id,
        action="exhibition.publish",
        target_type="event",
        target_id=event.id,
        note=f"{len(rows)} 条",
    )
    db.flush()
    return OkOut()


@router.post("/assignments/reset", response_model=OkOut, summary="取消整次抽取")
def reset_assignments(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> OkOut:
    event = company_service.current_event(db)
    for row in db.scalars(select(Introduction).where(Introduction.event_id == event.id)).all():
        db.delete(row)
    return OkOut()


@router.post(
    "/assignments/reveal-all", response_model=OkOut, summary="一次性标记全部介绍已完成"
)
def reveal_all(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> OkOut:
    event = company_service.current_event(db)
    now = datetime.now(UTC)
    for row in db.scalars(
        select(Introduction).where(
            Introduction.event_id == event.id, Introduction.status == "published"
        )
    ).all():
        row.given_at = now
    return OkOut()


# --- 审核与在场 -------------------------------------------------------------


@router.get(
    "/collaborations", response_model=list[CollaborationOut], summary="全部合作（含隐藏）"
)
def list_collaborations(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[CollaborationOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Collaboration)
        .where(Collaboration.event_id == event.id)
        .order_by(Collaboration.created_at.desc())
    ).all()
    return [
        CollaborationOut(
            id=row.id,
            from_company=_name(db, row.from_company_id),
            to_company=_name(db, row.to_company_id),
            kind=row.kind,
            body=row.body,
            status=row.status,
        )
        for row in rows
    ]


@router.get("/comments", response_model=list[AdminCommentOut], summary="评论审核（被举报优先）")
def list_comments(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[AdminCommentOut]:
    counts = dict(
        db.execute(
            select(CommentReport.comment_id, func.count(CommentReport.id)).group_by(
                CommentReport.comment_id
            )
        ).all()
    )
    rows = db.execute(
        select(Comment, User)
        .join(User, User.id == Comment.author_id)
        .order_by(Comment.created_at.desc())
        .limit(300)
    ).all()
    items = [
        AdminCommentOut(
            id=comment.id,
            project_id=comment.project_id,
            author_name=user.display_name,
            body=comment.body,
            status=comment.status,
            reports=int(counts.get(comment.id, 0)),
        )
        for comment, user in rows
    ]
    items.sort(key=lambda item: item.reports, reverse=True)
    return items


@router.post(
    "/introductions/{introduction_id}/moderation", response_model=OkOut, summary="隐藏或恢复介绍"
)
def moderate_introduction(
    introduction_id: str,
    payload: ModerationFlagIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _intro(db, introduction_id).status = "hidden" if payload.hidden else "published"
    return OkOut()


@router.post("/replies/{reply_id}/moderation", response_model=OkOut, summary="隐藏或恢复回复")
def moderate_reply(
    reply_id: str,
    payload: ModerationFlagIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _intro(db, reply_id).reply_status = "hidden" if payload.hidden else "visible"
    return OkOut()


@router.put("/presence/{company_id}", response_model=OkOut, summary="奖项日签到")
def set_presence(
    company_id: str,
    payload: PresenceIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = db.get(Company, company_id)
    if company is None:
        raise NotFound("公司不存在")
    company.present = payload.present
    return OkOut()


@router.get("/promotions", response_model=list[PromotionOut], summary="赛事全部广告")
def list_promotions(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[PromotionOut]:
    rows = db.execute(
        select(Promotion, Company)
        .join(Company, Company.id == Promotion.company_id)
        .order_by(Promotion.created_at.desc())
        .limit(200)
    ).all()
    return [
        PromotionOut(
            id=promotion.id,
            company_id=company.id,
            company_name=company.name,
            kind=promotion.kind,
            body=promotion.body,
            created_at=promotion.created_at,
        )
        for promotion, company in rows
    ]
