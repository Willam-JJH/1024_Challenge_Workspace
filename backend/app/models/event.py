"""赛事、公司（参赛队）、成员关系。"""

from datetime import datetime

from sqlalchemy import DateTime, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.models.base import BaseModel


class Event(BaseModel):
    __tablename__ = "events"

    slug: Mapped[str] = mapped_column(String(80), unique=True, index=True)
    name: Mapped[str] = mapped_column(String(160))
    # draft | registration | build | judging | results | archived
    stage: Mapped[str] = mapped_column(String(32), default="registration")
    starts_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    ends_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    registration_open: Mapped[bool] = mapped_column(default=True)
    project_seats: Mapped[int] = mapped_column(Integer, default=4)

    companies: Mapped[list["Company"]] = relationship(back_populates="event")


class Company(BaseModel):
    __tablename__ = "companies"
    __table_args__ = (UniqueConstraint("event_id", "slug", name="uq_companies_event_slug"),)

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    slug: Mapped[str] = mapped_column(String(80))
    name: Mapped[str] = mapped_column(String(120))
    blurb: Mapped[str | None] = mapped_column(Text, nullable=True)
    captain_id: Mapped[str | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True
    )
    roster_locked: Mapped[bool] = mapped_column(default=False)

    event: Mapped[Event] = relationship(back_populates="companies")
    memberships: Mapped[list["Membership"]] = relationship(back_populates="company")


class Membership(BaseModel):
    __tablename__ = "memberships"
    __table_args__ = (UniqueConstraint("company_id", "user_id", name="uq_membership"),)

    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    # member | captain | submitter
    role: Mapped[str] = mapped_column(String(32), default="member")

    company: Mapped[Company] = relationship(back_populates="memberships")
