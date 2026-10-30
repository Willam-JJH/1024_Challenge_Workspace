"""管理台：看板、搜索、审计与赛事开关。"""

from fastapi import APIRouter, Depends, Query
from sqlalchemy import func, or_, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.stubs import register_stubs
from app.models.audit import AuditLog
from app.models.event import Company
from app.models.exhibition import Comment
from app.models.identity import User
from app.models.offline import OfflineSession
from app.models.project import Project
from app.models.vote import Vote
from app.schemas.admin import AuditOut, DashboardOut, ScheduleIn, SearchHit, SwitchesIn
from app.schemas.common import EventOut
from app.services import audit
from app.services import companies as company_service

router = APIRouter(prefix="/admin", tags=["admin"])


def _count(db: DbSession, model, *conditions) -> int:  # noqa: ANN001
    stmt = select(func.count()).select_from(model)
    for condition in conditions:
        stmt = stmt.where(condition)
    return int(db.scalar(stmt) or 0)


@router.get("/dashboard", response_model=DashboardOut, summary="赛事体征看板")
def dashboard(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> DashboardOut:
    event = company_service.current_event(db)
    return DashboardOut(
        users=_count(db, User, User.status == "active"),
        companies=_count(db, Company, Company.event_id == event.id),
        projects_published=_count(db, Project, Project.status == "published"),
        projects_draft=_count(db, Project, Project.status == "draft"),
        coins=_count(db, Vote, Vote.event_id == event.id, Vote.status == "live"),
        comments=_count(db, Comment, Comment.status == "visible"),
        offline_sessions=_count(db, OfflineSession, OfflineSession.event_id == event.id),
    )


@router.get("/search", response_model=list[SearchHit], summary="命令面板：人、公司、作品")
def search(
    q: str = Query(min_length=1, max_length=120),
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[SearchHit]:
    pattern = f"%{q.lower()}%"
    hits: list[SearchHit] = []

    for user in db.scalars(
        select(User)
        .where(
            or_(func.lower(User.email).like(pattern), func.lower(User.display_name).like(pattern))
        )
        .limit(5)
    ).all():
        hits.append(SearchHit(kind="user", id=user.id, label=user.display_name, hint=user.email))

    for company in db.scalars(
        select(Company).where(func.lower(Company.name).like(pattern)).limit(5)
    ).all():
        hits.append(SearchHit(kind="company", id=company.id, label=company.name, hint=company.slug))

    for project in db.scalars(
        select(Project).where(func.lower(Project.title).like(pattern)).limit(5)
    ).all():
        hits.append(
            SearchHit(kind="project", id=project.id, label=project.title, hint=project.status)
        )
    return hits


@router.get("/audit", response_model=list[AuditOut], summary="审计日志（最新在前）")
def list_audit(
    limit: int = Query(default=100, ge=1, le=500),
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[AuditOut]:
    rows = db.scalars(select(AuditLog).order_by(AuditLog.created_at.desc()).limit(limit)).all()
    return [AuditOut.model_validate(row) for row in rows]


@router.get("/audit/actions", response_model=list[str], summary="日志里出现过的动作")
def audit_actions(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[str]:
    return list(db.scalars(select(AuditLog.action).distinct().order_by(AuditLog.action)).all())


@router.get("/event", response_model=EventOut, summary="当前赛事")
def get_event(_: Principal = Depends(require_staff), db: DbSession = Depends(get_db)) -> EventOut:
    return EventOut.model_validate(company_service.current_event(db))


@router.patch("/event/schedule", response_model=EventOut, summary="编辑日程与阶段")
def patch_schedule(
    payload: ScheduleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> EventOut:
    event = company_service.current_event(db)
    if payload.starts_at is not None:
        event.starts_at = payload.starts_at
    if payload.ends_at is not None:
        event.ends_at = payload.ends_at
    if payload.stage is not None:
        event.stage = payload.stage
    audit.record(
        db,
        actor_id=principal.user.id,
        action="event.schedule",
        target_type="event",
        target_id=event.id,
        note=payload.stage,
    )
    db.flush()
    return EventOut.model_validate(event)


@router.put("/event/switches", response_model=EventOut, summary="开关报名、投票与直播")
def put_switches(
    payload: SwitchesIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> EventOut:
    event = company_service.current_event(db)
    if payload.registration_open is not None:
        event.registration_open = payload.registration_open
    if payload.voting_open is not None:
        event.voting_open = payload.voting_open
    if payload.pitch_on_air is not None:
        event.pitch_on_air = payload.pitch_on_air
    audit.record(
        db,
        actor_id=principal.user.id,
        action="event.switches",
        target_type="event",
        target_id=event.id,
        note=f"registration={event.registration_open} voting={event.voting_open}",
    )
    db.flush()
    return EventOut.model_validate(event)


register_stubs(
    router,
    [
        ("GET", "/voting", "投票状态、可见性、选票统计与实时总数"),
        ("GET", "/voting/ballots", "谁投给了谁（仅管理员，永不公开）"),
        ("GET", "/voting/events", "每一次投币、撤回、归还与作废"),
        ("PUT", "/voting/visibility", "对公众显示或隐藏实时总数"),
        ("POST", "/voting/voids", "作废一枚硬币或一位投票人"),
        ("GET", "/exhibition", "展览总览：护照、介绍、评论、成交、广告"),
        ("GET", "/exhibition/assignments", "介绍任务：已发布、可分配、缺口、跳过"),
        ("PATCH", "/exhibition/assignments/{assignment_id}", "调整草稿分配"),
        ("POST", "/exhibition/assignments/{assignment_id}/cancellations", "取消分配"),
        ("POST", "/exhibition/assignments/{assignment_id}/given", "标记介绍已完成或撤销"),
        ("POST", "/exhibition/assignments/{assignment_id}/reassignments", "换一家公司介绍"),
        ("POST", "/exhibition/assignments/remaining", "为后来加入的公司补分配"),
        ("POST", "/exhibition/assignments/generation", "随机错位抽取（草稿）"),
        ("POST", "/exhibition/assignments/publications", "发布全部草稿并通知"),
        ("POST", "/exhibition/assignments/reset", "取消整次抽取"),
        ("POST", "/exhibition/assignments/reveal-all", "一次性标记全部介绍已完成"),
        ("GET", "/exhibition/collaborations", "赛事全部合作（含隐藏）"),
        ("GET", "/exhibition/comments", "待审核评论（被举报优先）"),
        ("POST", "/exhibition/introductions/{introduction_id}/moderation", "隐藏或恢复介绍"),
        ("POST", "/exhibition/replies/{reply_id}/moderation", "隐藏或恢复回复"),
        ("PUT", "/exhibition/presence/{company_id}", "奖项日签到"),
        ("GET", "/exhibition/promotions", "赛事全部广告"),
    ],
    tag="admin",
    auth="staff",
)
