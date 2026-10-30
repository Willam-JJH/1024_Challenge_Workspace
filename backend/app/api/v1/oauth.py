"""OAuth 2.1（授权码 + PKCE）、用量查询与已连接应用。"""

import json
from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Request
from fastapi.responses import RedirectResponse
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import ApiError, Conflict, NotFound
from app.models.identity import User
from app.models.llm import LlmModel, LlmUsage
from app.models.oauth import OAuthClient, OAuthConsent, OAuthToken
from app.schemas.common import OkOut
from app.services import oauth as oauth_service

oauth_router = APIRouter(prefix="/oauth", tags=["oauth"])
usage_router = APIRouter(prefix="/usage", tags=["usage"])
apps_router = APIRouter(prefix="/me/connected-apps", tags=["oauth"])
well_known_router = APIRouter(tags=["oauth"])


# --- 授权 -------------------------------------------------------------------


def _consented(db: DbSession, client_id: str, user_id: str) -> OAuthConsent | None:
    row = db.scalar(
        select(OAuthConsent).where(
            OAuthConsent.client_id == client_id, OAuthConsent.user_id == user_id
        )
    )
    return row if row is not None and row.revoked_at is None else None


def _redirect_with_code(
    db: DbSession,
    *,
    client_id: str,
    user_id: str,
    redirect_uri: str,
    code_challenge: str,
    scope: str,
    state: str | None,
) -> str:
    code = oauth_service.issue_code(
        db,
        client_id=client_id,
        user_id=user_id,
        redirect_uri=redirect_uri,
        code_challenge=code_challenge,
        scope=scope,
    )
    joiner = "&" if "?" in redirect_uri else "?"
    url = f"{redirect_uri}{joiner}code={code}"
    if state:
        url += f"&state={state}"
    return url


@oauth_router.get("/authorize", summary="授权端点（授权码 + PKCE S256）")
def authorize(
    client_id: str,
    redirect_uri: str,
    code_challenge: str,
    response_type: str = "code",
    code_challenge_method: str = "S256",
    scope: str = "profile",
    state: str | None = None,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
):
    client = oauth_service.client_or_404(db, client_id)
    oauth_service.check_redirect(client, redirect_uri)
    if response_type != "code" or code_challenge_method != "S256":
        raise ApiError(
            "只支持授权码 + PKCE S256", code="unsupported_response_type", status_code=400
        )
    if _consented(db, client_id, principal.user.id) is None:
        raise ApiError("需要用户先授权", code="consent_required", status_code=403)

    url = _redirect_with_code(
        db,
        client_id=client_id,
        user_id=principal.user.id,
        redirect_uri=redirect_uri,
        code_challenge=code_challenge,
        scope=scope,
        state=state,
    )
    return RedirectResponse(url, status_code=302)


@oauth_router.get("/consent", summary="授权确认页所需信息")
def consent_info(
    client_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> dict:
    client = oauth_service.client_or_404(db, client_id)
    return {
        "client_name": client.name,
        "scopes": client.scopes.split(),
        "approved": _consented(db, client_id, principal.user.id) is not None,
    }


@oauth_router.post("/consent", summary="允许或拒绝应用，返回浏览器跳转地址")
def consent(
    payload: dict,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> dict:
    client_id = payload.get("client_id") or ""
    redirect_uri = payload.get("redirect_uri") or ""
    client = oauth_service.client_or_404(db, client_id)
    oauth_service.check_redirect(client, redirect_uri)

    if not payload.get("approve", True):
        joiner = "&" if "?" in redirect_uri else "?"
        url = f"{redirect_uri}{joiner}error=access_denied"
        if payload.get("state"):
            url += f"&state={payload['state']}"
        return {"redirect_url": url}

    row = db.scalar(
        select(OAuthConsent).where(
            OAuthConsent.client_id == client_id, OAuthConsent.user_id == principal.user.id
        )
    )
    if row is None:
        row = OAuthConsent(
            client_id=client_id, user_id=principal.user.id, scope=payload.get("scope", "profile")
        )
        db.add(row)
    else:
        row.revoked_at = None
    db.flush()

    url = _redirect_with_code(
        db,
        client_id=client_id,
        user_id=principal.user.id,
        redirect_uri=redirect_uri,
        code_challenge=payload.get("code_challenge", ""),
        scope=payload.get("scope", "profile"),
        state=payload.get("state"),
    )
    return {"redirect_url": url}


async def _form_or_json(request: Request) -> dict:
    if request.headers.get("content-type", "").startswith("application/x-www-form-urlencoded"):
        return dict(await request.form())
    try:
        return await request.json()
    except ValueError:
        return {}


@oauth_router.post("/token", summary="令牌端点：授权码换令牌、刷新令牌轮换")
async def token(
    request: Request, db: DbSession = Depends(get_db)
) -> dict:
    payload = await _form_or_json(request)
    grant_type = payload.get("grant_type")
    client_id = payload.get("client_id") or ""

    if grant_type == "authorization_code":
        result = oauth_service.exchange_code(
            db,
            code=payload.get("code", ""),
            client_id=client_id,
            redirect_uri=payload.get("redirect_uri", ""),
            code_verifier=payload.get("code_verifier", ""),
        )
    elif grant_type == "refresh_token":
        result = oauth_service.rotate_refresh(
            db, refresh_token=payload.get("refresh_token", ""), client_id=client_id
        )
    else:
        raise ApiError("不支持的 grant_type", code="unsupported_grant_type", status_code=400)
    db.flush()
    return result


@oauth_router.post("/revoke", response_model=OkOut, summary="令牌吊销（RFC 7009）")
async def revoke(request: Request, db: DbSession = Depends(get_db)) -> OkOut:
    from app.core.security import hash_token

    payload = await _form_or_json(request)
    raw = payload.get("token", "")
    if raw:
        digest = hash_token(raw)
        row = db.scalar(
            select(OAuthToken).where(
                (OAuthToken.access_hash == digest) | (OAuthToken.refresh_hash == digest)
            )
        )
        if row is not None:
            row.revoked_at = datetime.now(UTC)
    return OkOut()


@oauth_router.get("/userinfo", summary="令牌归属（需 profile 作用域）")
def userinfo(request: Request, db: DbSession = Depends(get_db)) -> dict:
    from app.core.security import hash_token  # noqa: F401  (保持依赖显式)

    header = request.headers.get("authorization", "")
    if not header.lower().startswith("bearer "):
        raise ApiError("缺少令牌", code="invalid_token", status_code=401)
    row = oauth_service.resolve_access(db, header[7:].strip())
    user = db.get(User, row.user_id)
    if user is None:
        raise NotFound("账号不存在")
    return {"sub": user.id, "email": user.email, "name": user.display_name, "locale": user.locale}


@well_known_router.get(
    "/.well-known/oauth-authorization-server", summary="授权服务器元数据（RFC 8414）"
)
def metadata() -> dict:
    return {
        "issuer": "1024-challenge",
        "authorization_endpoint": "/api/v1/oauth/authorize",
        "token_endpoint": "/api/v1/oauth/token",
        "revocation_endpoint": "/api/v1/oauth/revoke",
        "userinfo_endpoint": "/api/v1/oauth/userinfo",
        "response_types_supported": ["code"],
        "grant_types_supported": ["authorization_code", "refresh_token"],
        "code_challenge_methods_supported": ["S256"],
        "scopes_supported": ["profile", "llm"],
    }


# --- 用量 -------------------------------------------------------------------


def _usage_rows(db: DbSession, user_id: str) -> list[LlmUsage]:
    return list(
        db.scalars(
            select(LlmUsage)
            .where(LlmUsage.user_id == user_id)
            .order_by(LlmUsage.created_at.desc())
            .limit(100)
        ).all()
    )


@usage_router.get("/summary", summary="我的 LLM 用量汇总")
def usage_summary(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> dict:
    rows = _usage_rows(db, principal.user.id)
    return {
        "requests": len(rows),
        "prompt_tokens": sum(row.prompt_tokens for row in rows),
        "completion_tokens": sum(row.completion_tokens for row in rows),
        "cost_micro": sum(row.cost_micro for row in rows),
    }


@usage_router.get("/requests", summary="我最近的 LLM 请求")
def usage_requests(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[dict]:
    rows = _usage_rows(db, principal.user.id)
    models = {
        row.id: row.name
        for row in db.scalars(
            select(LlmModel).where(LlmModel.id.in_({row.model_id for row in rows if row.model_id}))
        ).all()
    }
    return [
        {
            "id": row.id,
            "model": models.get(row.model_id or ""),
            "prompt_tokens": row.prompt_tokens,
            "completion_tokens": row.completion_tokens,
            "cost_micro": row.cost_micro,
            "created_at": row.created_at.isoformat(),
        }
        for row in rows
    ]


@usage_router.get("/team", summary="我公司的用量")
def usage_team(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> dict:
    from app.models.event import Company, Membership

    membership = db.scalar(select(Membership).where(Membership.user_id == principal.user.id))
    if membership is None:
        return {"company": None, "requests": 0, "cost_micro": 0}
    member_ids = select(Membership.user_id).where(
        Membership.company_id == membership.company_id
    )
    rows = db.scalars(
        select(LlmUsage).where(LlmUsage.user_id.in_(member_ids))
    ).all()
    company = db.get(Company, membership.company_id)
    return {
        "company": company.name if company else None,
        "requests": len(rows),
        "prompt_tokens": sum(row.prompt_tokens for row in rows),
        "completion_tokens": sum(row.completion_tokens for row in rows),
        "cost_micro": sum(row.cost_micro for row in rows),
    }


# --- 已连接应用 -------------------------------------------------------------


@apps_router.get("", summary="我用账号登录过的应用与设备")
def list_apps(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[dict]:
    rows = db.scalars(
        select(OAuthToken).where(
            OAuthToken.user_id == principal.user.id, OAuthToken.revoked_at.is_(None)
        )
    ).all()
    grouped: dict[str, list[dict]] = {}
    for row in rows:
        grouped.setdefault(row.client_id, []).append(
            {"device_id": row.device_id, "created_at": row.created_at.isoformat()}
        )
    out = []
    for client_id, devices in grouped.items():
        client = db.scalar(select(OAuthClient).where(OAuthClient.client_id == client_id))
        out.append(
            {
                "client_id": client_id,
                "client_name": client.name if client else client_id,
                "devices": devices,
            }
        )
    return out


@apps_router.delete("/{client_id}", response_model=OkOut, summary="吊销某个应用的全部设备")
def revoke_app(
    client_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    now = datetime.now(UTC)
    rows = db.scalars(
        select(OAuthToken).where(
            OAuthToken.user_id == principal.user.id,
            OAuthToken.client_id == client_id,
            OAuthToken.revoked_at.is_(None),
        )
    ).all()
    if not rows:
        raise NotFound("没有这个应用的授权")
    for row in rows:
        row.revoked_at = now
    consent = db.scalar(
        select(OAuthConsent).where(
            OAuthConsent.client_id == client_id, OAuthConsent.user_id == principal.user.id
        )
    )
    if consent is not None:
        consent.revoked_at = now
    return OkOut()


@apps_router.delete(
    "/{client_id}/devices/{device_id}", response_model=OkOut, summary="登出某个应用的单个设备"
)
def revoke_device(
    client_id: str,
    device_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    now = datetime.now(UTC)
    rows = db.scalars(
        select(OAuthToken).where(
            OAuthToken.user_id == principal.user.id,
            OAuthToken.client_id == client_id,
            OAuthToken.device_id == device_id,
            OAuthToken.revoked_at.is_(None),
        )
    ).all()
    if not rows:
        raise NotFound("设备不存在")
    for row in rows:
        row.revoked_at = now
    return OkOut()


__all__ = ["Conflict", "json"]
