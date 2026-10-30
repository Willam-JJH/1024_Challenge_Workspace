"""投票与榜单的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, Field


class PickIn(BaseModel):
    company_id: str


class VotesIn(BaseModel):
    company_ids: list[str] = Field(max_length=20)


class VotePickOut(BaseModel):
    company_id: str
    company_name: str
    company_slug: str
    created_at: datetime


class MyVotesOut(BaseModel):
    votes: list[VotePickOut]
    votes_left: int


class BoardRow(BaseModel):
    company_id: str
    name: str
    slug: str
    votes: int
    rank: int


class BoardOut(BaseModel):
    items: list[BoardRow]
    total_votes: int
    updated_at: datetime


class BallotOut(BaseModel):
    id: str
    voter_id: str
    voter_name: str
    company_id: str
    company_name: str
    status: str
    created_at: datetime


class VoidIn(BaseModel):
    voter_id: str | None = None
    company_id: str | None = None


class VisibilityIn(BaseModel):
    visible: bool


class VotingAdminOut(BaseModel):
    open: bool
    visible: bool
    total_votes: int
    ballots: int
    board: list[BoardRow]
