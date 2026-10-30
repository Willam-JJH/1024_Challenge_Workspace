"""邮件中心的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class BroadcastIn(BaseModel):
    title: str = Field(min_length=1, max_length=160)
    body: str = Field(min_length=1, max_length=16000)
    audience: str = Field(default="all", pattern="^(all|participants|members|staff)$")
    locale: str = Field(default="all", pattern="^(all|zh|en)$")


class BroadcastPatchIn(BaseModel):
    title: str | None = Field(default=None, max_length=160)
    body: str | None = Field(default=None, max_length=16000)
    audience: str | None = Field(default=None, pattern="^(all|participants|members|staff)$")
    locale: str | None = Field(default=None, pattern="^(all|zh|en)$")


class BroadcastOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    title: str
    body: str
    audience: str
    locale: str
    status: str
    scheduled_at: datetime | None = None
    sent_at: datetime | None = None


class ScheduleIn(BaseModel):
    scheduled_at: datetime | None = None


class PreviewIn(BaseModel):
    title: str
    body: str
    locale: str = "zh"


class PreviewOut(BaseModel):
    subject: str
    body: str


class EstimateIn(BaseModel):
    audience: str = Field(default="all", pattern="^(all|participants|members|staff)$")
    locale: str = Field(default="all", pattern="^(all|zh|en)$")


class EstimateOut(BaseModel):
    recipients: int
    by_locale: dict[str, int]


class MessageOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    to_email: str
    subject: str
    status: str
    error: str | None = None
    sent_at: datetime | None = None
    created_at: datetime


class MessagesOut(BaseModel):
    items: list[MessageOut]
    counts: dict[str, int]


class TemplateOut(BaseModel):
    key: str
    name: str
    description: str


class TemplatePreviewIn(BaseModel):
    template: str
    locale: str = "zh"
    props: dict[str, str] = {}


class SetupOut(BaseModel):
    driver: str
    daily_quota: int | None = None
    sent_today: int = 0
