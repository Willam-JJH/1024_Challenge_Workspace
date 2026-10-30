"""OpenAI 兼容网关：路由解析、预算与熔断、计量。"""

from datetime import UTC, datetime

import httpx
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.errors import ApiError, Forbidden, NotFound
from app.models.event import Event
from app.models.identity import User
from app.models.llm import LlmModel, LlmRoute, LlmUpstream, LlmUsage
from app.services import companies as company_service
from app.services import llm as llm_service


class BudgetExhausted(ApiError):
    status_code, code = 402, "budget_exhausted"


class UpstreamFailed(ApiError):
    status_code, code = 502, "upstream_failed"


def guard(db: DbSession, user: User) -> Event:
    """全局熔断、单人熔断与预算。"""
    event = company_service.current_event(db)
    if event.llm_kill_switch:
        raise ApiError("LLM 服务已暂停", code="llm_disabled", status_code=503)
    if user.llm_blocked:
        raise Forbidden("你的账号已被暂停使用 LLM")
    if event.llm_budget_micro > 0 and llm_service.spent(db) >= event.llm_budget_micro:
        raise BudgetExhausted("赛事 LLM 预算已用完")
    return event


def resolve(db: DbSession, model_name: str) -> tuple[LlmModel, LlmRoute, LlmUpstream]:
    model = db.scalar(select(LlmModel).where(LlmModel.name == model_name))
    if model is None or not model.enabled:
        raise NotFound("模型不存在或已停用")
    rows = db.execute(
        select(LlmRoute, LlmUpstream)
        .join(LlmUpstream, LlmUpstream.id == LlmRoute.upstream_id)
        .where(
            LlmRoute.model_id == model.id,
            LlmRoute.enabled.is_(True),
            LlmUpstream.enabled.is_(True),
        )
        .order_by(LlmRoute.priority)
    ).all()
    if not rows:
        raise UpstreamFailed("该模型没有可用的上游路由")
    route, upstream = rows[0]
    return model, route, upstream


def _http_post(url: str, headers: dict[str, str], payload: dict) -> tuple[int, dict]:
    with httpx.Client(timeout=120) as client:
        response = client.post(url, headers=headers, json=payload)
    try:
        body = response.json()
    except ValueError:
        body = {"error": {"message": response.text[:500]}}
    return response.status_code, body


def _cost_micro(model: LlmModel, prompt: int, completion: int) -> int:
    return (prompt * model.input_price_micro + completion * model.output_price_micro) // 1_000_000


def meter(
    db: DbSession,
    *,
    user_id: str,
    model: LlmModel,
    prompt_tokens: int,
    completion_tokens: int,
) -> int:
    cost = _cost_micro(model, prompt_tokens, completion_tokens)
    db.add(
        LlmUsage(
            user_id=user_id,
            model_id=model.id,
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            cost_micro=cost,
        )
    )
    return cost


def completion(db: DbSession, user: User, payload: dict) -> dict:
    """转发一次 chat/completions，并记录用量。"""
    if payload.get("stream"):
        raise ApiError(
            "流式转发尚未实现，请先使用 stream=false",
            code="stream_unsupported",
            status_code=501,
        )
    model_name = payload.get("model")
    if not model_name:
        raise ApiError("缺少 model 字段", code="invalid_request", status_code=422)

    guard(db, user)
    model, route, upstream = resolve(db, model_name)

    body = dict(payload)
    body["model"] = route.upstream_model
    headers = {"Authorization": f"Bearer {upstream.api_key}", "Content-Type": "application/json"}
    status, data = _http_post(f"{upstream.base_url.rstrip('/')}/v1/chat/completions", headers, body)
    if status >= 400:
        message = (data.get("error") or {}).get("message") or "上游返回错误"
        raise UpstreamFailed(f"{message}（HTTP {status}）")

    usage = data.get("usage") or {}
    meter(
        db,
        user_id=user.id,
        model=model,
        prompt_tokens=int(usage.get("prompt_tokens") or 0),
        completion_tokens=int(usage.get("completion_tokens") or 0),
    )
    data.setdefault("created", int(datetime.now(UTC).timestamp()))
    return data
