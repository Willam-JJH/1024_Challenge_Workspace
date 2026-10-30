"""OpenAI 兼容面，挂在 /v1（与业务接口的 /api/v1 区分）。

鉴权只接受 ``Authorization: Bearer``（API key）。请求经 ``app.services.gateway``
转发到上游（例如本机 new-api），并按 token 记账。
"""

from fastapi import APIRouter, Body, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import ApiError
from app.models.llm import LlmModel
from app.services import gateway

router = APIRouter(prefix="/v1", tags=["llm"])


@router.get("/models", summary="可用模型列表（OpenAI 兼容）")
def list_models(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> dict:
    gateway.guard(db, principal.user)
    rows = db.scalars(select(LlmModel).where(LlmModel.enabled.is_(True))).all()
    return {
        "object": "list",
        "data": [
            {
                "id": row.name,
                "object": "model",
                "owned_by": "1024-challenge",
                "context_window": row.context_window,
            }
            for row in rows
        ],
    }


@router.post("/chat/completions", summary="对话补全（转发到上游并计费）")
def chat_completions(
    payload: dict = Body(...),
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> dict:
    return gateway.completion(db, principal.user, payload)


@router.post("/embeddings", summary="向量化（预留）")
@router.post("/responses", summary="Responses API（预留）")
@router.post("/search", summary="联网检索（预留）")
def not_implemented() -> None:
    raise ApiError("该接口尚未实现", code="not_implemented", status_code=501)

