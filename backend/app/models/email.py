"""邮件：广播与发送日志。"""

from datetime import datetime

from sqlalchemy import ForeignKey, String, Text
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class Broadcast(BaseModel):
    __tablename__ = "broadcasts"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    title: Mapped[str] = mapped_column(String(160))
    body: Mapped[str] = mapped_column(Text)
    # all | participants | members | staff
    audience: Mapped[str] = mapped_column(String(32), default="all")
    locale: Mapped[str] = mapped_column(String(8), default="all")
    # draft | scheduled | sent | cancelled
    status: Mapped[str] = mapped_column(String(16), default="draft")
    scheduled_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    sent_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    created_by: Mapped[str | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True
    )


class EmailMessage(BaseModel):
    __tablename__ = "email_messages"

    broadcast_id: Mapped[str | None] = mapped_column(
        ForeignKey("broadcasts.id", ondelete="SET NULL"), nullable=True, index=True
    )
    to_email: Mapped[str] = mapped_column(String(320), index=True)
    subject: Mapped[str] = mapped_column(String(200))
    body: Mapped[str] = mapped_column(Text, default="")
    # queued | sent | failed
    status: Mapped[str] = mapped_column(String(16), default="queued")
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    sent_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
