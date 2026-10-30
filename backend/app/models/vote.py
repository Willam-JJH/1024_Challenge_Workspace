"""硬币投票：一人对一家公司最多一枚。"""

from sqlalchemy import ForeignKey, String, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel


class Vote(BaseModel):
    __tablename__ = "votes"
    __table_args__ = (UniqueConstraint("event_id", "voter_id", "company_id", name="uq_vote"),)

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    voter_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    # live | withdrawn | voided
    status: Mapped[str] = mapped_column(String(16), default="live")
