"""作品的请求与响应模型。"""

from datetime import datetime

from pydantic import BaseModel, Field

from app.schemas.common import ORMModel, ProjectOut


class ProjectDraftIn(BaseModel):
    base_revision: int = Field(ge=0)
    title: str = Field(default="", max_length=160)
    summary: str = Field(default="", max_length=2000)
    content: str = Field(default="", max_length=200_000)


class DraftSavedOut(BaseModel):
    revision: int
    updated_at: datetime


class EditSessionIn(BaseModel):
    take_over: bool = False


class EditSessionOut(BaseModel):
    id: str
    token: str
    expires_at: datetime
    heartbeat_seconds: int = 30


class VersionOut(ORMModel):
    id: str
    version: int
    title: str
    summary: str
    published_at: datetime


class PublishOut(BaseModel):
    project: ProjectOut
    version: VersionOut


class AttendanceIn(BaseModel):
    attendance: str = Field(pattern="^(yes|no|maybe)$")


class ProjectCardOut(ProjectOut):
    company_name: str
    company_slug: str
