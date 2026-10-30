"""「我」相关的资源：资料、参赛身份、邀请、通知、钱包、API key。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import NotFound
from app.core.security import hash_token, new_token
from app.core.stubs import register_stubs
from app.models.event import Company
from app.models.identity import ApiKey
from app.schemas.auth import ApiKeyIn, ProfileUpdateIn
from app.schemas.common import ApiKeyCreatedOut, ApiKeyOut, OkOut, UserOut
from app.schemas.vote import PickOut, WalletIn, WalletOut
from app.services import companies as company_service
from app.services import votes as vote_service

router = APIRouter(prefix="/me", tags=["me"])


@router.get("", response_model=UserOut, summary="我的账号")
def get_me(principal: Principal = Depends(require_auth)) -> UserOut:
    return UserOut.model_validate(principal.user)


@router.patch("", response_model=UserOut, summary="更新我的资料")
def update_me(
    payload: ProfileUpdateIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> UserOut:
    user = principal.user
    if payload.display_name is not None:
        user.display_name = payload.display_name
    if payload.locale is not None:
        user.locale = payload.locale
    db.add(user)
    db.flush()
    return UserOut.model_validate(user)


# --- API key ----------------------------------------------------------------


@router.get("/api-keys", response_model=list[ApiKeyOut], summary="我的 API key 列表")
def list_api_keys(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[ApiKeyOut]:
    rows = db.scalars(
        select(ApiKey)
        .where(ApiKey.user_id == principal.user.id, ApiKey.revoked_at.is_(None))
        .order_by(ApiKey.created_at.desc())
    ).all()
    return [ApiKeyOut.model_validate(row) for row in rows]


def _issue_key(db: DbSession, user_id: str, label: str) -> ApiKeyCreatedOut:
    token = "sk_" + new_token()
    row = ApiKey(user_id=user_id, label=label, prefix=token[:11], key_hash=hash_token(token))
    db.add(row)
    db.flush()
    return ApiKeyCreatedOut(key=ApiKeyOut.model_validate(row), token=token)


@router.post(
    "/api-keys",
    response_model=ApiKeyCreatedOut,
    status_code=201,
    summary="创建 API key（完整密钥只返回一次）",
)
def create_api_key(
    payload: ApiKeyIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> ApiKeyCreatedOut:
    return _issue_key(db, principal.user.id, payload.label)


@router.delete("/api-keys/{key_id}", response_model=OkOut, summary="吊销 API key")
def revoke_api_key(
    key_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(ApiKey, key_id)
    if row is None or row.user_id != principal.user.id:
        raise NotFound("API key 不存在")
    if row.revoked_at is None:
        row.revoked_at = datetime.now(UTC)
    return OkOut()


@router.post(
    "/api-keys/{key_id}/rotations",
    response_model=ApiKeyCreatedOut,
    summary="轮换 API key（旧密钥立即失效）",
)
def rotate_api_key(
    key_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> ApiKeyCreatedOut:
    row = db.get(ApiKey, key_id)
    if row is None or row.user_id != principal.user.id:
        raise NotFound("API key 不存在")
    row.revoked_at = datetime.now(UTC)
    return _issue_key(db, principal.user.id, row.label)


# --- 钱包（投币） -----------------------------------------------------------


def _wallet(db: DbSession, user_id: str) -> WalletOut:
    event = company_service.current_event(db)
    picks = vote_service.my_picks(db, event.id, user_id)
    rows = []
    for vote in picks:
        company = db.get(Company, vote.company_id)
        if company is None:
            continue
        rows.append(
            PickOut(
                company_id=company.id,
                company_name=company.name,
                company_slug=company.slug,
                created_at=vote.created_at,
            )
        )
    return WalletOut(picks=rows, picks_left=max(settings.wallet_max_picks - len(rows), 0))


@router.get("/wallet", response_model=WalletOut, summary="我的硬币：已投公司与余额")
def get_wallet(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> WalletOut:
    return _wallet(db, principal.user.id)


@router.put("/wallet", response_model=WalletOut, summary="整张选票一次性保存（最多三家公司）")
def put_wallet(
    payload: WalletIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> WalletOut:
    event = company_service.current_event(db)
    vote_service.replace_wallet(db, event, principal.user.id, payload.company_ids)
    db.flush()
    return _wallet(db, principal.user.id)


@router.put(
    "/wallet/coins/{company_id}", response_model=WalletOut, summary="投一枚硬币（幂等）"
)
def invest_coin(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> WalletOut:
    event = company_service.current_event(db)
    vote_service.invest(db, event, principal.user.id, company_id)
    db.flush()
    return _wallet(db, principal.user.id)


@router.delete(
    "/wallet/coins/{company_id}", response_model=WalletOut, summary="撤回硬币（幂等）"
)
def withdraw_coin(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> WalletOut:
    event = company_service.current_event(db)
    vote_service.withdraw(db, event, principal.user.id, company_id)
    db.flush()
    return _wallet(db, principal.user.id)


register_stubs(
    router,
    [
        ("GET", "/connected-apps", "我用账号登录过的应用与设备"),
        ("DELETE", "/connected-apps/{client_id}", "吊销某个应用的全部设备"),
        ("DELETE", "/connected-apps/{client_id}/devices/{device_id}", "登出某个应用的单个设备"),
    ],
    tag="me",
    auth="authenticated",
)
