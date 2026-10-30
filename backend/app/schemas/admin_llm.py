"""LLM 管理台的请求/响应模型。"""

from pydantic import BaseModel, ConfigDict, Field


class ModelIn(BaseModel):
    name: str = Field(min_length=1, max_length=120)
    input_price_micro: int = Field(default=0, ge=0)
    output_price_micro: int = Field(default=0, ge=0)
    context_window: int = Field(default=0, ge=0)
    enabled: bool = True


class ModelPatchIn(BaseModel):
    name: str | None = None
    input_price_micro: int | None = Field(default=None, ge=0)
    output_price_micro: int | None = Field(default=None, ge=0)
    context_window: int | None = Field(default=None, ge=0)
    enabled: bool | None = None


class ModelOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    name: str
    input_price_micro: int
    output_price_micro: int
    context_window: int
    enabled: bool


class UpstreamIn(BaseModel):
    name: str = Field(min_length=1, max_length=120)
    base_url: str = Field(min_length=4, max_length=500)
    api_key: str = Field(default="", max_length=500)
    priority: int = Field(default=100, ge=0, le=1000)
    enabled: bool = True


class UpstreamPatchIn(BaseModel):
    name: str | None = None
    base_url: str | None = Field(default=None, max_length=500)
    api_key: str | None = Field(default=None, max_length=500)
    priority: int | None = Field(default=None, ge=0, le=1000)
    enabled: bool | None = None


class UpstreamOut(BaseModel):
    id: str
    name: str
    base_url: str
    priority: int
    enabled: bool
    has_key: bool


class RouteIn(BaseModel):
    upstream_id: str
    upstream_model: str = Field(min_length=1, max_length=160)
    priority: int = Field(default=100, ge=0, le=1000)


class RouteOut(BaseModel):
    id: str
    model_id: str
    upstream_id: str
    upstream_name: str
    upstream_model: str
    priority: int
    enabled: bool


class BudgetIn(BaseModel):
    budget_micro: int = Field(ge=0)


class BudgetOut(BaseModel):
    budget_micro: int
    spent_micro: int
    remaining_micro: int


class OverviewOut(BudgetOut):
    requests: int
    burn_24h_micro: int
    models: int
    upstreams: int


class KillSwitchOut(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    global_: bool = Field(alias="global")
    disabled_models: list[str]
    blocked_users: list[str]


class FlagIn(BaseModel):
    enabled: bool


class UsageRowOut(BaseModel):
    label: str
    prompt_tokens: int
    completion_tokens: int
    cost_micro: int
