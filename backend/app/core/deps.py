"""鉴权依赖。

两种凭据统一解析为 ``Principal``：

- 浏览器：``sid`` HttpOnly cookie（会话令牌）
- 程序访问：``Authorization: Bearer <token>``（API key，前缀 ``sk_``；或会话令牌）

会话令牌与 API key 在数据库中都只存 SHA-256 哈希。
"""

from dataclasses import dataclass

from fastapi import Depends, Request
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.errors import Forbidden, Unauthorized
from app.core.security import hash_token
from app.models.base import utcnow
from app.models.identity import ApiKey, Session, User

ROLE_RANK = {"participant": 0, "member": 1, "captain": 2, "staff": 3, "admin": 4}


@dataclass(slots=True)
class Principal:
    user: User
    via: str  # cookie | api_key
    session_id: str | None = None


def _bearer(request: Request) -> str | None:
    header = request.headers.get("authorization", "")
    if not header.lower().startswith("bearer "):
        return None
    return header[7:].strip() or None


def _session_by_token(db: DbSession, token: str) -> Session | None:
    row = db.scalar(select(Session).where(Session.token_hash == hash_token(token)))
    if row is None or row.revoked_at is not None:
        return None
    row.last_seen_at = utcnow()
    return row


def get_principal_optional(request: Request, db: DbSession = Depends(get_db)) -> Principal | None:
    token = _bearer(request)
    if token:
        if token.startswith("sk_"):
            key = db.scalar(select(ApiKey).where(ApiKey.key_hash == hash_token(token)))
            if key is None or key.revoked_at is not None:
                raise Unauthorized("API key 无效或已吊销")
            key.last_used_at = utcnow()
            user = db.get(User, key.user_id)
            if user is None or user.status != "active":
                raise Unauthorized("账号不可用")
            return Principal(user=user, via="api_key")
        session = _session_by_token(db, token)
        if session is not None:
            user = db.get(User, session.user_id)
            if user is not None and user.status == "active":
                return Principal(user=user, via="cookie", session_id=session.id)

    cookie = request.cookies.get(settings.session_cookie)
    if cookie:
        session = _session_by_token(db, cookie)
        if session is not None:
            user = db.get(User, session.user_id)
            if user is not None and user.status == "active":
                return Principal(user=user, via="cookie", session_id=session.id)
    return None


def require_auth(principal: Principal | None = Depends(get_principal_optional)) -> Principal:
    if principal is None:
        raise Unauthorized("需要登录")
    return principal


def require_staff(principal: Principal = Depends(require_auth)) -> Principal:
    if ROLE_RANK.get(principal.user.role, 0) < ROLE_RANK["staff"]:
        raise Forbidden("需要组织者权限")
    return principal


def require_admin(principal: Principal = Depends(require_auth)) -> Principal:
    if principal.user.role != "admin":
        raise Forbidden("需要管理员权限")
    return principal


def require_scope(scope: str):  # noqa: ANN201 - 依赖工厂
    """细粒度权限：按角色等级校验（后续可扩展为权限表）。"""

    def _dep(principal: Principal = Depends(require_auth)) -> Principal:
        needed = {"read": 0, "write": 1, "manage": 3, "admin": 4}[scope]
        if ROLE_RANK.get(principal.user.role, 0) < needed:
            raise Forbidden(f"缺少权限：{scope}")
        return principal

    return _dep
