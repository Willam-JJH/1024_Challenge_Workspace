"""线下场次的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class SessionOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    title: str
    description: str | None = None
    location: str | None = None
    starts_at: datetime
    ends_at: datetime | None = None
    seats: int
    status: str


class SessionCreateIn(BaseModel):
    title: str = Field(min_length=1, max_length=160)
    description: str | None = Field(default=None, max_length=2000)
    location: str | None = Field(default=None, max_length=200)
    starts_at: datetime
    ends_at: datetime | None = None
    seats: int = Field(default=30, ge=1, le=500)


class SessionPatchIn(BaseModel):
    title: str | None = Field(default=None, max_length=160)
    description: str | None = Field(default=None, max_length=2000)
    location: str | None = Field(default=None, max_length=200)
    starts_at: datetime | None = None
    ends_at: datetime | None = None
    seats: int | None = Field(default=None, ge=1, le=500)


class RsvpIn(BaseModel):
    answer: str = Field(pattern="^(going|maybe|no)$")


class RsvpOut(BaseModel):
    session_id: str
    answer: str | None = None
    waitlisted: bool = False
    checked_in: bool = False


class SessionWithMineOut(SessionOut):
    mine: RsvpOut | None = None
    going_count: int = 0
    seats_left: int = 0


class AdminRsvpOut(BaseModel):
    id: str
    user_id: str
    user_name: str
    answer: str
    waitlisted: bool
    checked_in_at: datetime | None = None
    created_at: datetime


class CheckInIn(BaseModel):
    checked_in: bool = True
