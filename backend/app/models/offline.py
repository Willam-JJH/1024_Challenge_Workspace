"""线下场次与报名（候补、签到）。"""

from datetime import datetime

from sqlalchemy import Boolean, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class OfflineSession(BaseModel):
    __tablename__ = "offline_sessions"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    title: Mapped[str] = mapped_column(String(160))
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    location: Mapped[str | None] = mapped_column(String(200), nullable=True)
    starts_at: Mapped[datetime] = mapped_column(UTCDateTime())
    ends_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    seats: Mapped[int] = mapped_column(Integer, default=30)
    # scheduled | cancelled
    status: Mapped[str] = mapped_column(String(16), default="scheduled")


class OfflineRsvp(BaseModel):
    __tablename__ = "offline_rsvps"
    __table_args__ = (UniqueConstraint("session_id", "user_id", name="uq_offline_rsvp"),)

    session_id: Mapped[str] = mapped_column(
        ForeignKey("offline_sessions.id", ondelete="CASCADE"), index=True
    )
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    # going | maybe | no
    answer: Mapped[str] = mapped_column(String(8), default="going")
    waitlisted: Mapped[bool] = mapped_column(Boolean, default=False)
    checked_in_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
