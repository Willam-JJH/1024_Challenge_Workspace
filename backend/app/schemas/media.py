"""素材的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class AssetCreateIn(BaseModel):
    filename: str = Field(min_length=1, max_length=200)
    content_type: str = Field(default="application/octet-stream", max_length=120)
    size: int = Field(default=0, ge=0)
    purpose: str = Field(default="submission", pattern="^(submission|avatar|sponsor_logo|pitch)$")


class AssetOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    filename: str
    content_type: str
    size: int
    purpose: str
    alt: str | None = None
    status: str
    created_at: datetime


class AssetCreatedOut(BaseModel):
    asset: AssetOut
    upload_url: str


class AltIn(BaseModel):
    alt: str = Field(max_length=300)
