"""跨模块复用的响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, EmailStr


class ORMModel(BaseModel):
    model_config = ConfigDict(from_attributes=True)


class UserOut(ORMModel):
    id: str
    email: EmailStr
    display_name: str
    locale: str
    role: str
    status: str
    created_at: datetime


class SessionOut(ORMModel):
    id: str
    user_agent: str | None = None
    client_ip: str | None = None
    last_seen_at: datetime
    created_at: datetime


class EventOut(ORMModel):
    id: str
    slug: str
    name: str
    stage: str
    starts_at: datetime | None = None
    ends_at: datetime | None = None
    registration_open: bool
    project_seats: int


class CompanyOut(ORMModel):
    id: str
    slug: str
    name: str
    blurb: str | None = None
    captain_id: str | None = None
    roster_locked: bool


class ProjectOut(ORMModel):
    id: str
    company_id: str
    status: str
    title: str
    summary: str
    content: str
    revision: int
    live_version: int | None = None


class OkOut(BaseModel):
    ok: bool = True


class HealthOut(BaseModel):
    status: str
    version: str
    database: str
