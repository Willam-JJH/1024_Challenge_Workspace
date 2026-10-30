"""展览：评论、广告、护照与介绍的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, Field


class CommentIn(BaseModel):
    body: str = Field(min_length=1, max_length=2000)
    parent_id: str | None = None


class CommentOut(BaseModel):
    id: str
    project_id: str
    author_id: str
    author_name: str
    parent_id: str | None = None
    body: str
    created_at: datetime


class PromotionIn(BaseModel):
    kind: str = Field(default="wall", pattern="^(wall|ticker)$")
    body: str = Field(min_length=1, max_length=280)


class PromotionOut(BaseModel):
    id: str
    company_id: str
    company_name: str
    kind: str
    body: str
    created_at: datetime


class VisitIn(BaseModel):
    note: str | None = Field(default=None, max_length=280)


class VisitOut(BaseModel):
    project_id: str
    company_name: str
    project_title: str
    note: str | None = None
    created_at: datetime


class PassportOut(BaseModel):
    items: list[VisitOut]
    needed: int
    complete: bool


class StampOut(BaseModel):
    visitor_name: str
    note: str | None = None
    created_at: datetime


class IntroductionOut(BaseModel):
    id: str
    role: str  # introducer | target
    introducer_company: str
    target_company: str
    text: str | None = None
    reply: str | None = None
    status: str


class IntroductionTextIn(BaseModel):
    text: str = Field(min_length=1, max_length=1200)


class IntroductionReplyIn(BaseModel):
    reply: str = Field(min_length=1, max_length=1200)


class FeedItem(BaseModel):
    type: str
    title: str
    company_name: str
    at: datetime
