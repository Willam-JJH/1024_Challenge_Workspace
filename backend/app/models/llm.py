"""LLM 网关：模型、上游、路由与用量。"""

from sqlalchemy import Boolean, ForeignKey, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel


class LlmModel(BaseModel):
    __tablename__ = "llm_models"

    name: Mapped[str] = mapped_column(String(120), unique=True, index=True)
    input_price_micro: Mapped[int] = mapped_column(Integer, default=0)   # 每百万 token 的微美元
    output_price_micro: Mapped[int] = mapped_column(Integer, default=0)
    context_window: Mapped[int] = mapped_column(Integer, default=0)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)


class LlmUpstream(BaseModel):
    """OpenAI 兼容上游（例如本机 new-api）。密钥只写，不回显。"""

    __tablename__ = "llm_upstreams"

    name: Mapped[str] = mapped_column(String(120))
    base_url: Mapped[str] = mapped_column(Text)
    api_key: Mapped[str] = mapped_column(Text, default="")
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    priority: Mapped[int] = mapped_column(Integer, default=100)


class LlmRoute(BaseModel):
    __tablename__ = "llm_routes"
    __table_args__ = (UniqueConstraint("model_id", "upstream_id", name="uq_llm_route"),)

    model_id: Mapped[str] = mapped_column(
        ForeignKey("llm_models.id", ondelete="CASCADE"), index=True
    )
    upstream_id: Mapped[str] = mapped_column(
        ForeignKey("llm_upstreams.id", ondelete="CASCADE"), index=True
    )
    upstream_model: Mapped[str] = mapped_column(String(160))
    priority: Mapped[int] = mapped_column(Integer, default=100)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)


class LlmUsage(BaseModel):
    __tablename__ = "llm_usage"

    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    model_id: Mapped[str | None] = mapped_column(
        ForeignKey("llm_models.id", ondelete="SET NULL"), nullable=True, index=True
    )
    prompt_tokens: Mapped[int] = mapped_column(Integer, default=0)
    completion_tokens: Mapped[int] = mapped_column(Integer, default=0)
    cost_micro: Mapped[int] = mapped_column(Integer, default=0)
