"""评审打分、入围名单与奖项。"""

from datetime import datetime

from sqlalchemy import ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class Score(BaseModel):
    """一位评委对一个作品的评分卡。"""

    __tablename__ = "scores"
    __table_args__ = (UniqueConstraint("project_id", "judge_id", name="uq_score"),)

    project_id: Mapped[str] = mapped_column(
        ForeignKey("projects.id", ondelete="CASCADE"), index=True
    )
    judge_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    # 三个维度各 1–10
    creativity: Mapped[int] = mapped_column(Integer, default=0)
    engineering: Mapped[int] = mapped_column(Integer, default=0)
    presentation: Mapped[int] = mapped_column(Integer, default=0)
    notes: Mapped[str | None] = mapped_column(Text, nullable=True)
    submitted_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class ShortlistEntry(BaseModel):
    """已公布的入围名单，按组织者顺序。"""

    __tablename__ = "shortlist_entries"
    __table_args__ = (UniqueConstraint("event_id", "project_id", name="uq_shortlist"),)

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    project_id: Mapped[str] = mapped_column(
        ForeignKey("projects.id", ondelete="CASCADE"), index=True
    )
    position: Mapped[int] = mapped_column(Integer, default=0)
    announced_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class Award(BaseModel):
    """公布的奖项：每个奖项指向一个作品。"""

    __tablename__ = "awards"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    kind: Mapped[str] = mapped_column(String(32))
    title: Mapped[str] = mapped_column(String(120))
    project_id: Mapped[str] = mapped_column(
        ForeignKey("projects.id", ondelete="CASCADE"), index=True
    )
    announced_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
