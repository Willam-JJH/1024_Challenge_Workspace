"""管理台：线下场次与签到。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.identity import User
from app.models.offline import OfflineRsvp, OfflineSession
from app.schemas.common import OkOut
from app.schemas.offline import (
    AdminRsvpOut,
    CheckInIn,
    SessionCreateIn,
    SessionPatchIn,
    SessionWithMineOut,
)
from app.services import companies as company_service
from app.services import offline as offline_service
from app.services.mailer import get_mailer

router = APIRouter(prefix="/admin/offline-sessions", tags=["admin"])


def _session(db: DbSession, session_id: str) -> OfflineSession:
    row = db.get(OfflineSession, session_id)
    if row is None:
        raise NotFound("场次不存在")
    return row


def _brief(db: DbSession, row: OfflineSession) -> SessionWithMineOut:
    return SessionWithMineOut(
        id=row.id,
        title=row.title,
        description=row.description,
        location=row.location,
        starts_at=row.starts_at,
        ends_at=row.ends_at,
        seats=row.seats,
        status=row.status,
        mine=None,
        going_count=offline_service.going_count(db, row.id),
        seats_left=offline_service.seats_left(db, row),
    )


@router.get("", response_model=list[SessionWithMineOut], summary="场次与报名数")
def list_sessions(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[SessionWithMineOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(OfflineSession)
        .where(OfflineSession.event_id == event.id)
        .order_by(OfflineSession.starts_at)
    ).all()
    return [_brief(db, row) for row in rows]


@router.post("", response_model=SessionWithMineOut, status_code=201, summary="新建场次")
def create_session(
    payload: SessionCreateIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SessionWithMineOut:
    event = company_service.current_event(db)
    row = OfflineSession(
        event_id=event.id,
        title=payload.title,
        description=payload.description,
        location=payload.location,
        starts_at=payload.starts_at,
        ends_at=payload.ends_at,
        seats=payload.seats,
    )
    db.add(row)
    db.flush()
    return _brief(db, row)


@router.patch("/{session_id}", response_model=SessionWithMineOut, summary="编辑场次")
def patch_session(
    session_id: str,
    payload: SessionPatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SessionWithMineOut:
    row = _session(db, session_id)
    for field in ("title", "description", "location", "starts_at", "ends_at", "seats"):
        value = getattr(payload, field)
        if value is not None:
            setattr(row, field, value)
    db.flush()
    offline_service.promote_waitlist(db, row)
    return _brief(db, row)


@router.delete("/{session_id}", response_model=OkOut, summary="删除无人报名的场次")
def delete_session(
    session_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _session(db, session_id)
    count = int(
        db.scalar(
            select(func.count())
            .select_from(OfflineRsvp)
            .where(OfflineRsvp.session_id == row.id)
        )
        or 0
    )
    if count:
        raise Conflict("已经有人报名，请先取消场次")
    db.delete(row)
    return OkOut()


@router.post("/{session_id}/cancellations", response_model=OkOut, summary="取消场次并通知")
def cancel_session(
    session_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _session(db, session_id)
    row.status = "cancelled"
    for rsvp in db.scalars(
        select(OfflineRsvp).where(OfflineRsvp.session_id == row.id)
    ).all():
        user = db.get(User, rsvp.user_id)
        if user is not None:
            get_mailer().send_notice(
                to=user.email,
                subject=f"{row.title} 已取消",
                body="这场线下活动已取消，请留意后续通知。",
            )
    db.flush()
    return OkOut()


@router.get("/{session_id}/rsvps", response_model=list[AdminRsvpOut], summary="报名名单")
def list_rsvps(
    session_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[AdminRsvpOut]:
    row = _session(db, session_id)
    rows = db.execute(
        select(OfflineRsvp, User)
        .join(User, User.id == OfflineRsvp.user_id)
        .where(OfflineRsvp.session_id == row.id)
        .order_by(OfflineRsvp.waitlisted, OfflineRsvp.created_at)
    ).all()
    return [
        AdminRsvpOut(
            id=rsvp.id,
            user_id=rsvp.user_id,
            user_name=user.display_name,
            answer=rsvp.answer,
            waitlisted=rsvp.waitlisted,
            checked_in_at=rsvp.checked_in_at,
            created_at=rsvp.created_at,
        )
        for rsvp, user in rows
    ]


@router.put(
    "/{session_id}/rsvps/{rsvp_id}/check-in", response_model=OkOut, summary="现场签到／撤销"
)
def check_in(
    session_id: str,
    rsvp_id: str,
    payload: CheckInIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _session(db, session_id)
    rsvp = db.get(OfflineRsvp, rsvp_id)
    if rsvp is None or rsvp.session_id != session_id:
        raise NotFound("报名记录不存在")
    rsvp.checked_in_at = datetime.now(UTC) if payload.checked_in else None
    return OkOut()
