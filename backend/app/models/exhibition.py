"""展览：评论、举报、广告、护照印章与介绍任务。"""

from datetime import datetime

from sqlalchemy import ForeignKey, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class Comment(BaseModel):
    __tablename__ = "comments"

    project_id: Mapped[str] = mapped_column(
        ForeignKey("projects.id", ondelete="CASCADE"), index=True
    )
    author_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    parent_id: Mapped[str | None] = mapped_column(
        ForeignKey("comments.id", ondelete="CASCADE"), nullable=True, index=True
    )
    body: Mapped[str] = mapped_column(Text)
    # visible | hidden | deleted
    status: Mapped[str] = mapped_column(String(16), default="visible")


class CommentReport(BaseModel):
    __tablename__ = "comment_reports"
    __table_args__ = (UniqueConstraint("comment_id", "reporter_id", name="uq_comment_report"),)

    comment_id: Mapped[str] = mapped_column(
        ForeignKey("comments.id", ondelete="CASCADE"), index=True
    )
    reporter_id: Mapped[str] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), index=True
    )


class Promotion(BaseModel):
    """广告：广告墙或跑马灯。"""

    __tablename__ = "promotions"

    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    # wall | ticker
    kind: Mapped[str] = mapped_column(String(16), default="wall")
    body: Mapped[str] = mapped_column(Text)
    # live | hidden
    status: Mapped[str] = mapped_column(String(16), default="live")
    expires_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class Visit(BaseModel):
    """参观护照印章：一位访客对一家公司的一个作品最多一枚。"""

    __tablename__ = "visits"
    __table_args__ = (
        UniqueConstraint("event_id", "project_id", "visitor_id", name="uq_visit"),
    )

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    project_id: Mapped[str] = mapped_column(
        ForeignKey("projects.id", ondelete="CASCADE"), index=True
    )
    visitor_id: Mapped[str] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), index=True
    )
    note: Mapped[str | None] = mapped_column(Text, nullable=True)


class Introduction(BaseModel):
    """奖项日的公司介绍任务：介绍方写正文，被介绍方写回复。"""

    __tablename__ = "introductions"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    introducer_company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    target_company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    text: Mapped[str | None] = mapped_column(Text, nullable=True)
    reply: Mapped[str | None] = mapped_column(Text, nullable=True)
    # visible | hidden
    reply_status: Mapped[str] = mapped_column(String(16), default="visible")
    # draft | published | cancelled
    status: Mapped[str] = mapped_column(String(16), default="draft")
    given_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class Collaboration(BaseModel):
    """合作或假想收购：A 公司向 B 公司发起。"""

    __tablename__ = "collaborations"

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    from_company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    to_company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    # collaboration | acquisition
    kind: Mapped[str] = mapped_column(String(16), default="collaboration")
    body: Mapped[str] = mapped_column(Text, default="")
    # proposed | published | hidden
    status: Mapped[str] = mapped_column(String(16), default="published")
