"""OAuth 的签发与校验。"""

import base64
import hashlib
import json
import secrets
from datetime import UTC, datetime, timedelta

from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.errors import ApiError, NotFound
from app.core.security import hash_token, new_token
from app.models.oauth import AuthorizationCode, OAuthClient, OAuthToken
from app.models.types import UTCDateTime

CODE_TTL = timedelta(minutes=10)
ACCESS_TTL = timedelta(hours=1)
REFRESH_TTL = timedelta(days=30)


def client_or_404(db: DbSession, client_id: str) -> OAuthClient:
    row = db.scalar(select(OAuthClient).where(OAuthClient.client_id == client_id))
    if row is None:
        raise NotFound("应用不存在")
    return row


def redirect_uris(client: OAuthClient) -> list[str]:
    try:
        return list(json.loads(client.redirect_uris))
    except ValueError:
        return []


def check_redirect(client: OAuthClient, redirect_uri: str) -> None:
    if redirect_uri not in redirect_uris(client):
        raise ApiError("redirect_uri 未登记", code="invalid_request", status_code=400)


def verify_pkce(verifier: str, challenge: str) -> bool:
    digest = hashlib.sha256(verifier.encode()).digest()
    expected = base64.urlsafe_b64encode(digest).decode().rstrip("=")
    return secrets.compare_digest(expected, challenge)


def issue_code(
    db: DbSession,
    *,
    client_id: str,
    user_id: str,
    redirect_uri: str,
    code_challenge: str,
    scope: str,
) -> str:
    raw = new_token()
    db.add(
        AuthorizationCode(
            client_id=client_id,
            user_id=user_id,
            redirect_uri=redirect_uri,
            code_hash=hash_token(raw),
            code_challenge=code_challenge,
            scope=scope,
            expires_at=datetime.now(UTC) + CODE_TTL,
        )
    )
    return raw


def issue_tokens(
    db: DbSession, *, client_id: str, user_id: str, scope: str, device_id: str
) -> dict:
    access = new_token()
    refresh = new_token("rt_")
    now = datetime.now(UTC)
    db.add(
        OAuthToken(
            client_id=client_id,
            user_id=user_id,
            device_id=device_id,
            access_hash=hash_token(access),
            refresh_hash=hash_token(refresh),
            scope=scope,
            expires_at=now + ACCESS_TTL,
        )
    )
    return {
        "access_token": access,
        "refresh_token": refresh,
        "token_type": "Bearer",
        "expires_in": int(ACCESS_TTL.total_seconds()),
        "scope": scope,
    }


def resolve_access(db: DbSession, token: str) -> OAuthToken:
    row = db.scalar(select(OAuthToken).where(OAuthToken.access_hash == hash_token(token)))
    if row is None or row.revoked_at is not None:
        raise ApiError("令牌无效", code="invalid_token", status_code=401)
    if row.expires_at < datetime.now(UTC):
        raise ApiError("令牌已过期", code="invalid_token", status_code=401)
    return row


def exchange_code(
    db: DbSession, *, code: str, client_id: str, redirect_uri: str, code_verifier: str
) -> dict:
    row = db.scalar(
        select(AuthorizationCode).where(AuthorizationCode.code_hash == hash_token(code))
    )
    if row is None or row.consumed_at is not None:
        raise ApiError("授权码无效", code="invalid_grant", status_code=400)
    if row.client_id != client_id or row.redirect_uri != redirect_uri:
        raise ApiError("授权码与请求不匹配", code="invalid_grant", status_code=400)
    if row.expires_at < datetime.now(UTC):
        raise ApiError("授权码已过期", code="invalid_grant", status_code=400)
    if not verify_pkce(code_verifier, row.code_challenge):
        raise ApiError("PKCE 校验失败", code="invalid_grant", status_code=400)

    row.consumed_at = datetime.now(UTC)
    return issue_tokens(
        db,
        client_id=row.client_id,
        user_id=row.user_id,
        scope=row.scope,
        device_id=row.id,
    )


def rotate_refresh(db: DbSession, *, refresh_token: str, client_id: str) -> dict:
    row = db.scalar(select(OAuthToken).where(OAuthToken.refresh_hash == hash_token(refresh_token)))
    if row is None or row.revoked_at is not None:
        raise ApiError("刷新令牌无效", code="invalid_grant", status_code=400)
    if row.client_id != client_id:
        raise ApiError("刷新令牌与客户端不匹配", code="invalid_grant", status_code=400)
    if row.created_at + REFRESH_TTL < datetime.now(UTC):
        raise ApiError("刷新令牌已过期", code="invalid_grant", status_code=400)

    row.revoked_at = datetime.now(UTC)
    return issue_tokens(
        db,
        client_id=row.client_id,
        user_id=row.user_id,
        scope=row.scope,
        device_id=row.device_id,
    )


__all__ = ["UTCDateTime"]
