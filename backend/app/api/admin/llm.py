"""管理台：LLM 网关的预算、模型、上游、路由、熔断与用量。"""

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.identity import User
from app.models.llm import LlmModel, LlmRoute, LlmUpstream
from app.schemas.admin_llm import (
    BudgetIn,
    BudgetOut,
    FlagIn,
    KillSwitchOut,
    ModelIn,
    ModelOut,
    ModelPatchIn,
    OverviewOut,
    RouteIn,
    RouteOut,
    UpstreamIn,
    UpstreamOut,
    UpstreamPatchIn,
    UsageRowOut,
)
from app.schemas.common import OkOut
from app.services import audit
from app.services import companies as company_service
from app.services import llm as llm_service

router = APIRouter(prefix="/admin/llm", tags=["admin"])


def _budget(db: DbSession) -> BudgetOut:
    event = company_service.current_event(db)
    spent = llm_service.spent(db)
    return BudgetOut(
        budget_micro=event.llm_budget_micro,
        spent_micro=spent,
        remaining_micro=max(event.llm_budget_micro - spent, 0),
    )


@router.get("/overview", response_model=OverviewOut, summary="花费、余额、燃烧速率")
def overview(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> OverviewOut:
    base = _budget(db)
    return OverviewOut(
        **base.model_dump(),
        requests=llm_service.requests_count(db),
        burn_24h_micro=llm_service.burn_24h(db),
        models=int(db.scalar(select(func.count()).select_from(LlmModel)) or 0),
        upstreams=int(db.scalar(select(func.count()).select_from(LlmUpstream)) or 0),
    )


@router.get("/budget", response_model=BudgetOut, summary="赛事预算")
def get_budget(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> BudgetOut:
    return _budget(db)


@router.put("/budget", response_model=BudgetOut, summary="设置赛事预算（微美元）")
def set_budget(
    payload: BudgetIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> BudgetOut:
    event = company_service.current_event(db)
    event.llm_budget_micro = payload.budget_micro
    audit.record(
        db,
        actor_id=principal.user.id,
        action="llm.budget",
        target_type="event",
        target_id=event.id,
        note=str(payload.budget_micro),
    )
    return _budget(db)


# --- 模型 -------------------------------------------------------------------


@router.get("/models", response_model=list[ModelOut], summary="模型列表")
def list_models(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[ModelOut]:
    rows = db.scalars(select(LlmModel).order_by(LlmModel.name)).all()
    return [ModelOut.model_validate(row) for row in rows]


@router.post("/models", response_model=ModelOut, status_code=201, summary="新增公开模型")
def create_model(
    payload: ModelIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> ModelOut:
    if db.scalar(select(LlmModel).where(LlmModel.name == payload.name)):
        raise Conflict("同名模型已存在")
    row = LlmModel(**payload.model_dump())
    db.add(row)
    db.flush()
    return ModelOut.model_validate(row)


def _model(db: DbSession, model_id: str) -> LlmModel:
    row = db.get(LlmModel, model_id)
    if row is None:
        raise NotFound("模型不存在")
    return row


@router.patch("/models/{model_id}", response_model=ModelOut, summary="编辑模型")
def patch_model(
    model_id: str,
    payload: ModelPatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> ModelOut:
    row = _model(db, model_id)
    for field in ("name", "input_price_micro", "output_price_micro", "context_window", "enabled"):
        value = getattr(payload, field)
        if value is not None:
            setattr(row, field, value)
    db.flush()
    return ModelOut.model_validate(row)


@router.delete("/models/{model_id}", response_model=OkOut, summary="删除模型")
def delete_model(
    model_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    db.delete(_model(db, model_id))
    return OkOut()


# --- 上游 -------------------------------------------------------------------


def _upstream_out(row: LlmUpstream) -> UpstreamOut:
    return UpstreamOut(
        id=row.id,
        name=row.name,
        base_url=row.base_url,
        priority=row.priority,
        enabled=row.enabled,
        has_key=bool(row.api_key),
    )


@router.get("/upstreams", response_model=list[UpstreamOut], summary="上游列表（密钥掩码）")
def list_upstreams(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[UpstreamOut]:
    rows = db.scalars(select(LlmUpstream).order_by(LlmUpstream.priority, LlmUpstream.name)).all()
    return [_upstream_out(row) for row in rows]


@router.post("/upstreams", response_model=UpstreamOut, status_code=201, summary="新增上游")
def create_upstream(
    payload: UpstreamIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> UpstreamOut:
    row = LlmUpstream(**payload.model_dump())
    db.add(row)
    db.flush()
    return _upstream_out(row)


def _upstream(db: DbSession, upstream_id: str) -> LlmUpstream:
    row = db.get(LlmUpstream, upstream_id)
    if row is None:
        raise NotFound("上游不存在")
    return row


@router.patch("/upstreams/{upstream_id}", response_model=UpstreamOut, summary="编辑上游")
def patch_upstream(
    upstream_id: str,
    payload: UpstreamPatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> UpstreamOut:
    row = _upstream(db, upstream_id)
    for field in ("name", "base_url", "api_key", "priority", "enabled"):
        value = getattr(payload, field)
        if value is not None:
            setattr(row, field, value)
    db.flush()
    return _upstream_out(row)


@router.delete("/upstreams/{upstream_id}", response_model=OkOut, summary="删除上游及其路由")
def delete_upstream(
    upstream_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    db.delete(_upstream(db, upstream_id))
    return OkOut()


# --- 路由 -------------------------------------------------------------------


@router.post(
    "/models/{model_id}/routes", response_model=RouteOut, status_code=201, summary="添加路由"
)
def create_route(
    model_id: str,
    payload: RouteIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> RouteOut:
    model = _model(db, model_id)
    upstream = _upstream(db, payload.upstream_id)
    if db.scalar(
        select(LlmRoute).where(
            LlmRoute.model_id == model.id, LlmRoute.upstream_id == upstream.id
        )
    ):
        raise Conflict("该模型已有指向这个上游的路由")
    row = LlmRoute(
        model_id=model.id,
        upstream_id=upstream.id,
        upstream_model=payload.upstream_model,
        priority=payload.priority,
    )
    db.add(row)
    db.flush()
    return RouteOut(
        id=row.id,
        model_id=model.id,
        upstream_id=upstream.id,
        upstream_name=upstream.name,
        upstream_model=row.upstream_model,
        priority=row.priority,
        enabled=row.enabled,
    )


@router.patch("/routes/{route_id}", response_model=OkOut, summary="编辑路由")
def patch_route(
    route_id: str,
    payload: FlagIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(LlmRoute, route_id)
    if row is None:
        raise NotFound("路由不存在")
    row.enabled = payload.enabled
    return OkOut()


@router.delete("/routes/{route_id}", response_model=OkOut, summary="删除路由")
def delete_route(
    route_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(LlmRoute, route_id)
    if row is None:
        raise NotFound("路由不存在")
    db.delete(row)
    return OkOut()


# --- 熔断 -------------------------------------------------------------------


@router.get("/kill-switches", response_model=KillSwitchOut, summary="熔断开关状态")
def kill_switches(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> KillSwitchOut:
    event = company_service.current_event(db)
    return KillSwitchOut(
        global_=event.llm_kill_switch,
        disabled_models=[
            row.id for row in db.scalars(select(LlmModel).where(LlmModel.enabled.is_(False))).all()
        ],
        blocked_users=[
            row.id for row in db.scalars(select(User).where(User.llm_blocked.is_(True))).all()
        ],
    )


@router.put("/kill-switches/global", response_model=OkOut, summary="总开关（enabled=false 即熔断）")
def set_global_switch(
    payload: FlagIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    event = company_service.current_event(db)
    event.llm_kill_switch = not payload.enabled
    audit.record(
        db,
        actor_id=principal.user.id,
        action="llm.kill_switch",
        target_type="event",
        target_id=event.id,
        note=str(payload.enabled),
    )
    return OkOut()


@router.put("/kill-switches/models/{model_id}", response_model=OkOut, summary="单模型开关")
def set_model_switch(
    model_id: str,
    payload: FlagIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _model(db, model_id).enabled = payload.enabled
    return OkOut()


@router.put("/kill-switches/users/{user_id}", response_model=OkOut, summary="单人开关")
def set_user_switch(
    user_id: str,
    payload: FlagIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = db.get(User, user_id)
    if user is None:
        raise NotFound("账号不存在")
    user.llm_blocked = not payload.enabled
    return OkOut()


# --- 用量 -------------------------------------------------------------------


@router.get("/usage/teams", response_model=list[UsageRowOut], summary="按公司统计消耗")
def usage_teams(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[UsageRowOut]:
    return [UsageRowOut(**row) for row in llm_service.usage_by_team(db)]


@router.get("/usage/users", response_model=list[UsageRowOut], summary="按人统计消耗")
def usage_users(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[UsageRowOut]:
    return [UsageRowOut(**row) for row in llm_service.usage_by_user(db)]

