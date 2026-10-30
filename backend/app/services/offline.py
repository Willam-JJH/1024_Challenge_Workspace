"""线下场次的公共逻辑。"""

from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.models.offline import OfflineRsvp, OfflineSession


def going_count(db: DbSession, session_id: str) -> int:
    return int(
        db.scalar(
            select(func.count())
            .select_from(OfflineRsvp)
            .where(
                OfflineRsvp.session_id == session_id,
                OfflineRsvp.answer == "going",
                OfflineRsvp.waitlisted.is_(False),
            )
        )
        or 0
    )


def seats_left(db: DbSession, session: OfflineSession) -> int:
    return max(session.seats - going_count(db, session.id), 0)


def promote_waitlist(db: DbSession, session: OfflineSession) -> None:
    """座位变多时按报名先后把候补转正。"""
    waiting = db.scalars(
        select(OfflineRsvp)
        .where(
            OfflineRsvp.session_id == session.id,
            OfflineRsvp.answer == "going",
            OfflineRsvp.waitlisted.is_(True),
        )
        .order_by(OfflineRsvp.created_at)
    ).all()
    for rsvp in waiting:
        if seats_left(db, session) <= 0:
            break
        rsvp.waitlisted = False
        db.flush()
