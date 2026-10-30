"""评审与结果的请求/响应模型。"""

from datetime import datetime

from pydantic import BaseModel, Field

from app.schemas.common import ProjectOut
from app.schemas.vote import BoardRow


class ScoreIn(BaseModel):
    creativity: int = Field(ge=1, le=10)
    engineering: int = Field(ge=1, le=10)
    presentation: int = Field(ge=1, le=10)
    notes: str | None = Field(default=None, max_length=2000)
    submit: bool = False


class ScoreOut(BaseModel):
    project_id: str
    judge_id: str
    creativity: int
    engineering: int
    presentation: int
    notes: str | None = None
    submitted_at: datetime | None = None


class JudgingProjectOut(BaseModel):
    project: ProjectOut
    company_name: str
    my_score: ScoreOut | None = None


class ShortlistOut(BaseModel):
    project_id: str
    project_title: str
    company_name: str
    position: int


class AwardIn(BaseModel):
    kind: str = Field(min_length=1, max_length=32)
    title: str = Field(min_length=1, max_length=120)
    project_id: str


class AwardOut(BaseModel):
    id: str
    kind: str
    title: str
    project_id: str
    project_title: str
    company_name: str


class ResultsOut(BaseModel):
    announced: bool
    awards: list[AwardOut]
    board: list[BoardRow]


class AdvanceIn(BaseModel):
    top_n: int = Field(default=8, ge=1, le=50)
