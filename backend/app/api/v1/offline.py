"""线下场次：列表、报名（含候补）与日历导出。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Response
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, get_principal_optional, require_auth
from app.core.errors import Conflict, NotFound
from app.models.offline import OfflineRsvp, OfflineSession
from app.schemas.common import OkOut
from app.schemas.offline import (
    RsvpIn,
    RsvpOut,
    SessionCreateIn,
    SessionOut,
    SessionWithMineOut,
)
from app.services import companies as company_service
from app.services import offline as offline_service

router = APIRouter(prefix="/offline-sessions", tags=["offline"])


def _my_rsvp(db: DbSession, session_id: str, user_id: str | None) -> OfflineRsvp | None:
    if user_id is None:
        return None
    return db.scalar(
        select(OfflineRsvp).where(
            OfflineRsvp.session_id == session_id, OfflineRsvp.user_id == user_id
        )
    )


def _with_mine(
    db: DbSession, session: OfflineSession, user_id: str | None
) -> SessionWithMineOut:
    rsvp = _my_rsvp(db, session.id, user_id)
    return SessionWithMineOut(
        **SessionOut.model_validate(session).model_dump(),
        mine=(
            RsvpOut(
                session_id=session.id,
                answer=rsvp.answer,
                waitlisted=rsvp.waitlisted,
                checked_in=rsvp.checked_in_at is not None,
            )
            if rsvp is not None
            else None
        ),
        going_count=offline_service.going_count(db, session.id),
        seats_left=offline_service.seats_left(db, session),
    )


@router.get("", response_model=list[SessionWithMineOut], summary="线下场次（带我的回答）")
def list_sessions(
    principal: Principal | None = Depends(get_principal_optional),
    db: DbSession = Depends(get_db),
) -> list[SessionWithMineOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(OfflineSession)
        .where(OfflineSession.event_id == event.id)
        .order_by(OfflineSession.starts_at)
    ).all()
    user_id = principal.user.id if principal else None
    return [_with_mine(db, row, user_id) for row in rows]


def _session(db: DbSession, session_id: str) -> OfflineSession:
    row = db.get(OfflineSession, session_id)
    if row is None:
        raise NotFound("场次不存在")
    return row


@router.get("/{session_id}", response_model=SessionWithMineOut, summary="单个线下场次")
def get_session(
    session_id: str,
    principal: Principal | None = Depends(get_principal_optional),
    db: DbSession = Depends(get_db),
) -> SessionWithMineOut:
    row = _session(db, session_id)
    return _with_mine(db, row, principal.user.id if principal else None)


def _ics_datetime(value: datetime | None) -> str:
    if value is None:
        return ""
    return value.astimezone(UTC).strftime("%Y%m%dT%H%M%SZ")


@router.get("/{session_id}/calendar.ics", summary="导出为 iCalendar")
def calendar(session_id: str, db: DbSession = Depends(get_db)) -> Response:
    row = _session(db, session_id)
    stamp = _ics_datetime(datetime.now(UTC))
    lines = [
        "BEGIN:VCALENDAR",
        "VERSION:2.0",
        "PRODID:-//1024 Challenge Workspace//ZH",
        "BEGIN:VEVENT",
        f"UID:{row.id}@1024-challenge",
        f"DTSTAMP:{stamp}",
        f"DTSTART:{_ics_datetime(row.starts_at)}",
    ]
    if row.ends_at is not None:
        lines.append(f"DTEND:{_ics_datetime(row.ends_at)}")
    lines.append(f"SUMMARY:{row.title}")
    if row.location:
        lines.append(f"LOCATION:{row.location}")
    if row.description:
        lines.append(f"DESCRIPTION:{row.description.replace(chr(10), ' ')}")
    lines += ["END:VEVENT", "END:VCALENDAR", ""]
    return Response(
        "\r\n".join(lines),
        media_type="text/calendar; charset=utf-8",
        headers={"Content-Disposition": f'attachment; filename="{row.id}.ics"'},
    )


@router.put("/{session_id}/rsvp", response_model=RsvpOut, summary="报名（满员进入候补）")
def rsvp(
    session_id: str,
    payload: RsvpIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> RsvpOut:
    session = _session(db, session_id)
    if session.status != "scheduled":
        raise Conflict("场次已取消")
    if session.starts_at < datetime.now(UTC):
        raise Conflict("场次已开始")

    row = _my_rsvp(db, session.id, principal.user.id)
    if payload.answer == "no":
        if row is not None:
            db.delete(row)
        return RsvpOut(session_id=session.id, answer="no")

    waitlisted = payload.answer == "going" and offline_service.seats_left(db, session) <= 0
    if row is None:
        row = OfflineRsvp(
            session_id=session.id,
            user_id=principal.user.id,
            answer=payload.answer,
            waitlisted=waitlisted,
        )
        db.add(row)
    else:
        row.answer = payload.answer
        row.waitlisted = waitlisted
    db.flush()
    return RsvpOut(
        session_id=session.id,
        answer=row.answer,
        waitlisted=row.waitlisted,
        checked_in=row.checked_in_at is not None,
    )


@router.delete("/{session_id}/rsvp", response_model=OkOut, summary="取消报名")
def cancel_rsvp(
    session_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    session = _session(db, session_id)
    row = _my_rsvp(db, session.id, principal.user.id)
    if row is not None:
        db.delete(row)
        db.flush()
        offline_service.promote_waitlist(db, session)
    return OkOut()


__all__ = ["SessionCreateIn"]
