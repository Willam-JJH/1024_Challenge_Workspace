"""直播页的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class SourceOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    kind: str
    name: str
    url: str
    region: str
    enabled: bool
    position: int


class SourceIn(BaseModel):
    kind: str = Field(pattern="^(bilibili|youtube|twitch)$")
    name: str = Field(min_length=1, max_length=80)
    url: str = Field(min_length=4, max_length=500)
    region: str = Field(default="cn", pattern="^(cn|global)$")
    enabled: bool = True


class SourcePatchIn(BaseModel):
    name: str | None = Field(default=None, max_length=80)
    url: str | None = Field(default=None, max_length=500)
    region: str | None = Field(default=None, pattern="^(cn|global)$")
    enabled: bool | None = None


class MoveIn(BaseModel):
    direction: str = Field(pattern="^(up|down)$")


class MessageIn(BaseModel):
    body: str = Field(min_length=1, max_length=280)


class MessageOut(BaseModel):
    id: str
    author_id: str
    author_name: str
    body: str
    created_at: datetime


class ModerationIn(BaseModel):
    hidden: bool


class PitchStateOut(BaseModel):
    on_air: bool
    title: str
    sources: list[SourceOut]
    messages: list[MessageOut]


class PitchPatchIn(BaseModel):
    title: str | None = Field(default=None, max_length=160)
    on_air: bool | None = None
