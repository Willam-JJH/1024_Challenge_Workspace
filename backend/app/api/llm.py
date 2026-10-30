"""OpenAI 兼容的 LLM 网关面。

单独挂在 ``/v1``（与业务接口的 ``/api/v1`` 区分），只接受 Bearer 鉴权，
只讲 OpenAI 协议，不记录提示词与回复。
"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/v1", tags=["llm"])

register_stubs(
    router,
    [
        ("GET", "/models", "可用模型列表（OpenAI 兼容）"),
        ("POST", "/chat/completions", "对话补全（支持流式）"),
        ("POST", "/embeddings", "向量化"),
        ("POST", "/responses", "Responses API"),
        ("POST", "/search", "联网检索"),
    ],
    tag="llm",
    auth="authenticated",
)
