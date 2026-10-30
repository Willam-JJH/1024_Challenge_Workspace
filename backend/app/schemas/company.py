"""公司、成员、邀请相关的请求与响应模型。"""

from datetime import datetime

from pydantic import BaseModel, EmailStr, Field

from app.schemas.common import CompanyOut, ORMModel


class CompanyCreateIn(BaseModel):
    name: str = Field(min_length=2, max_length=120)
    blurb: str | None = Field(default=None, max_length=500)


class CompanyUpdateIn(BaseModel):
    name: str | None = Field(default=None, min_length=2, max_length=120)
    blurb: str | None = Field(default=None, max_length=500)


class MemberOut(BaseModel):
    user_id: str
    display_name: str
    role: str


class CompanyDetailOut(CompanyOut):
    members: list[MemberOut]
    seats_left: int


class InviteMemberIn(BaseModel):
    email: EmailStr


class InvitationOut(ORMModel):
    id: str
    email: EmailStr
    status: str
    created_at: datetime
    expires_at: datetime | None = None


class InvitationCreatedOut(BaseModel):
    invitation: InvitationOut
    token: str


class JoinRequestIn(BaseModel):
    message: str | None = Field(default=None, max_length=280)


class JoinRequestOut(ORMModel):
    id: str
    user_id: str
    message: str | None = None
    status: str
    created_at: datetime


class InviteLinkOut(BaseModel):
    token: str
    expires_at: datetime | None = None


class TransferIn(BaseModel):
    user_id: str


class RosterLockIn(BaseModel):
    locked: bool


class ActivationPreviewOut(BaseModel):
    email: EmailStr
    company_name: str
    event_name: str


class CompanySummaryOut(CompanyOut):
    member_count: int
    seats_left: int


class InvitationPreviewOut(BaseModel):
    kind: str  # invitation | link
    email: EmailStr | None = None
    company_name: str
    event_name: str


class InvitationPreviewIn(BaseModel):
    token: str = Field(min_length=16)
