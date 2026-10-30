"""管理台的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, EmailStr, Field


class UserRow(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    email: EmailStr
    display_name: str
    role: str
    status: str
    mode: str
    locale: str
    created_at: datetime


class MembershipBrief(BaseModel):
    company_id: str
    company_name: str
    role: str


class UserDetail(UserRow):
    memberships: list[MembershipBrief] = []


class RoleIn(BaseModel):
    role: str = Field(pattern="^(participant|member|captain|staff|admin)$")


class ModeIn(BaseModel):
    mode: str = Field(pattern="^(online|in_person|both)$")


class LocaleIn(BaseModel):
    locale: str = Field(min_length=2, max_length=8)


class BulkIdsIn(BaseModel):
    ids: list[str] = Field(min_length=1, max_length=500)


class BulkRoleIn(BulkIdsIn):
    role: str = Field(pattern="^(participant|member|captain|staff|admin)$")


class BulkLocaleIn(BulkIdsIn):
    locale: str = Field(min_length=2, max_length=8)


class CreateUserIn(BaseModel):
    email: EmailStr
    display_name: str = Field(min_length=1, max_length=120)
    locale: str = Field(default="zh", max_length=8)


class FeatureFlagIn(BaseModel):
    key: str = Field(pattern="^[a-z0-9][a-z0-9-]{1,60}$")
    description: str | None = Field(default=None, max_length=500)
    enabled: bool = False


class FeatureFlagPatchIn(BaseModel):
    description: str | None = Field(default=None, max_length=500)
    enabled: bool | None = None


class FeatureFlagOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    key: str
    description: str | None = None
    enabled: bool


class AnnouncementIn(BaseModel):
    title: str = Field(min_length=1, max_length=160)
    body: str = Field(min_length=1, max_length=20000)
    publish: bool = False


class AnnouncementOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    title: str
    body: str
    status: str
    published_at: datetime | None = None
    created_at: datetime


class SponsorIn(BaseModel):
    name: str = Field(min_length=1, max_length=120)
    logo_url: str | None = Field(default=None, max_length=500)
    website: str | None = Field(default=None, max_length=500)


class SponsorOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    name: str
    logo_url: str | None = None
    website: str | None = None
    position: int


class SponsorOrderIn(BaseModel):
    ids: list[str] = Field(min_length=1, max_length=200)


class SwitchesIn(BaseModel):
    registration_open: bool | None = None
    voting_open: bool | None = None
    pitch_on_air: bool | None = None


class ScheduleIn(BaseModel):
    starts_at: datetime | None = None
    ends_at: datetime | None = None
    stage: str | None = Field(default=None, max_length=32)


class DashboardOut(BaseModel):
    users: int
    companies: int
    projects_published: int
    projects_draft: int
    coins: int
    comments: int
    offline_sessions: int


class SearchHit(BaseModel):
    kind: str
    id: str
    label: str
    hint: str | None = None


class AuditOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    actor_id: str | None = None
    action: str
    target_type: str
    target_id: str | None = None
    note: str | None = None
    created_at: datetime
