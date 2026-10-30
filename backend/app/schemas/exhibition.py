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


class AssignmentOut(BaseModel):
    id: str
    introducer_company: str
    target_company: str
    status: str
    given_at: datetime | None = None
    has_text: bool = False
    has_reply: bool = False


class AssignTargetIn(BaseModel):
    target_company_id: str


class ModerationFlagIn(BaseModel):
    hidden: bool


class PresenceIn(BaseModel):
    present: bool


class CollaborationOut(BaseModel):
    id: str
    from_company: str
    to_company: str
    kind: str
    body: str
    status: str


class AdminCommentOut(BaseModel):
    id: str
    project_id: str
    author_name: str
    body: str
    status: str
    reports: int


class ExhibitionOverviewOut(BaseModel):
    companies: int
    published_introductions: int
    draft_introductions: int
    comments: int
    reported_comments: int
    visits: int
    live_promotions: int


class GivenIn(BaseModel):
    given: bool = True
