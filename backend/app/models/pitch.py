"""直播：多区域流源与页面留言。"""

from sqlalchemy import Boolean, ForeignKey, Integer, String, Text
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel


class PitchSource(BaseModel):
    __tablename__ = "pitch_sources"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    # bilibili | youtube | twitch
    kind: Mapped[str] = mapped_column(String(16))
    name: Mapped[str] = mapped_column(String(80))
    url: Mapped[str] = mapped_column(Text)
    # cn | global
    region: Mapped[str] = mapped_column(String(16), default="cn")
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    position: Mapped[int] = mapped_column(Integer, default=0)


class PitchMessage(BaseModel):
    __tablename__ = "pitch_messages"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    author_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    body: Mapped[str] = mapped_column(String(280))
    # visible | hidden
    status: Mapped[str] = mapped_column(String(16), default="visible")
