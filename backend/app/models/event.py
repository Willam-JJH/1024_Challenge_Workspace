"""赛事、公司（参赛队）、成员关系。"""

from datetime import datetime

from sqlalchemy import Boolean, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class Event(BaseModel):
    __tablename__ = "events"

    slug: Mapped[str] = mapped_column(String(80), unique=True, index=True)
    name: Mapped[str] = mapped_column(String(160))
    # draft | registration | build | judging | results | archived
    stage: Mapped[str] = mapped_column(String(32), default="registration")
    starts_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    ends_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    # 赛季正式收口的时刻：比赛结束满一个月由后台任务写入，之后作品进入前序作品、
    # 报名与投票关闭、并拒绝一切写入。手工的 stage 只做展示，不参与系统判断。
    archived_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    registration_open: Mapped[bool] = mapped_column(default=True)
    voting_open: Mapped[bool] = mapped_column(default=True)
    # 比赛结束后是否仍允许团队提交作品新版本。关掉之后 ends_at 一过作品即只读。
    post_event_edits: Mapped[bool] = mapped_column(Boolean, default=True)
    votes_visible: Mapped[bool] = mapped_column(default=True)
    # LLM 网关
    llm_budget_micro: Mapped[int] = mapped_column(Integer, default=0)
    llm_kill_switch: Mapped[bool] = mapped_column(Boolean, default=False)
    # 直播页
    pitch_title: Mapped[str] = mapped_column(String(160), default="Pitching Day")
    pitch_on_air: Mapped[bool] = mapped_column(default=False)
    company_seats: Mapped[int] = mapped_column(Integer, default=4)

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
    # 奖项日是否到场
    present: Mapped[bool] = mapped_column(default=False)

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


class EventEnrollment(BaseModel):
    """按届的报名记录：这一届谁报名成了参赛者、谁报名成了观众。

    唯一约束保证一人一届只有一条记录；取消报名是把 status 改成 withdrawn，
    不删行，报名历史要留下来。
    """

    __tablename__ = "event_enrollments"
    __table_args__ = (UniqueConstraint("event_id", "user_id", name="uq_event_enrollment"),)

    event_id: Mapped[str] = mapped_column(ForeignKey("events.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    # participant | audience
    kind: Mapped[str] = mapped_column(String(16), default="audience")
    # active | withdrawn
    status: Mapped[str] = mapped_column(String(16), default="active")


class Invitation(BaseModel):
    """邮件邀请：可以邀请已有账号，也可以邀请新人（激活时建号）。"""

    __tablename__ = "invitations"
    __table_args__ = (UniqueConstraint("token_hash", name="uq_invitations_token"),)

    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    email: Mapped[str] = mapped_column(String(320), index=True)
    token_hash: Mapped[str] = mapped_column(String(64))
    invited_by: Mapped[str | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True
    )
    # pending | accepted | declined | revoked
    status: Mapped[str] = mapped_column(String(16), default="pending")
    expires_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class JoinRequest(BaseModel):
    __tablename__ = "join_requests"
    __table_args__ = (UniqueConstraint("company_id", "user_id", name="uq_join_request"),)

    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    message: Mapped[str | None] = mapped_column(Text, nullable=True)
    # pending | accepted | declined | withdrawn
    status: Mapped[str] = mapped_column(String(16), default="pending")


class InviteLink(BaseModel):
    """可分享的邀请链接：按公司生效，可随时停用。"""

    __tablename__ = "invite_links"

    company_id: Mapped[str] = mapped_column(
        ForeignKey("companies.id", ondelete="CASCADE"), index=True
    )
    token_hash: Mapped[str] = mapped_column(String(64), index=True)
    created_by: Mapped[str | None] = mapped_column(
        ForeignKey("users.id", ondelete="SET NULL"), nullable=True
    )
    disabled_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
