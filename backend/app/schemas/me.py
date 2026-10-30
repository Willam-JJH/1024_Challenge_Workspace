"""个人中心的响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class MembershipOut(BaseModel):
    company_id: str
    company_name: str
    company_slug: str
    role: str
    captain: bool


class ParticipationIn(BaseModel):
    mode: str = Field(pattern="^(audience|participant)$")


class NotificationOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    kind: str
    title: str
    body: str | None = None
    read_at: datetime | None = None
    created_at: datetime


class DigestOut(BaseModel):
    comments: bool


class DigestPatchIn(BaseModel):
    comments: bool


class DeletionOut(BaseModel):
    requested_at: datetime | None = None


class LinkPreviewIn(BaseModel):
    url: str = Field(min_length=4, max_length=500)


class LinkPreviewOut(BaseModel):
    url: str
    title: str | None = None
    description: str | None = None
    image: str | None = None


class GithubRepoOut(BaseModel):
    full_name: str
    description: str | None = None
    stars: int
    language: str | None = None
    url: str


class CspReportIn(BaseModel):
    report: dict


class InvitationRow(BaseModel):
    invitation_id: str
    company_id: str
    company_name: str
    email: EmailStr
    status: str
