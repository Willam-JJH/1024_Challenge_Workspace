"""会话与口令。

新的鉴权语义：

- ``POST /auth/sessions`` 登录，令牌写入 ``sid`` HttpOnly cookie，同时回传一次供 CLI 使用
- ``DELETE /auth/sessions/current`` 退出当前设备；``DELETE /auth/sessions`` 退出全部设备
- 口令重置走「申请 → 校验码换授权 → 设置新口令」三段，不再复用登录接口
"""

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends, Request, Response
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, Unauthorized
from app.core.net import client_ip
from app.core.security import hash_password, hash_token, new_token, verify_password
from app.core.stubs import register_stubs
from app.models.identity import Session, User
from app.schemas.auth import LoginIn, PasswordChangeIn, SessionResponse
from app.schemas.common import OkOut, SessionOut, UserOut

router = APIRouter(prefix="/auth", tags=["auth"])


def _set_session_cookie(response: Response, token: str) -> None:
    response.set_cookie(
        settings.session_cookie,
        token,
        max_age=settings.session_ttl_days * 24 * 3600,
        httponly=True,
        samesite="lax",
        secure=settings.cookie_secure,
        domain=settings.cookie_domain,
        path="/",
    )


def _create_session(db: DbSession, user: User, request: Request) -> tuple[Session, str]:
    token = new_token()
    row = Session(
        user_id=user.id,
        token_hash=hash_token(token),
        user_agent=(request.headers.get("user-agent") or "")[:400] or None,
        client_ip=client_ip(request),
    )
    db.add(row)
    db.flush()
    return row, token


@router.post("/sessions", response_model=SessionResponse, summary="登录（签发会话）")
def login(
    payload: LoginIn,
    request: Request,
    response: Response,
    db: DbSession = Depends(get_db),
) -> SessionResponse:
    user = db.scalar(select(User).where(User.email == payload.email.lower()))
    if user is None or not verify_password(payload.password, user.password_hash):
        raise Unauthorized("邮箱或口令不正确")
    if user.status != "active":
        raise Unauthorized("账号尚未激活或已被停用")

    session, token = _create_session(db, user, request)
    _set_session_cookie(response, token)
    expires = datetime.now(UTC) + timedelta(days=settings.session_ttl_days)
    return SessionResponse(user=UserOut.model_validate(user), token=token, expires_at=expires)


@router.delete("/sessions/current", response_model=OkOut, summary="退出当前设备")
def logout(
    response: Response,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if principal.session_id:
        row = db.get(Session, principal.session_id)
        if row is not None:
            row.revoked_at = datetime.now(UTC)
    response.delete_cookie(settings.session_cookie, path="/")
    return OkOut()


@router.delete("/sessions", response_model=OkOut, summary="退出全部设备")
def logout_everywhere(
    response: Response,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    rows = db.scalars(
        select(Session).where(Session.user_id == principal.user.id, Session.revoked_at.is_(None))
    ).all()
    now = datetime.now(UTC)
    for row in rows:
        row.revoked_at = now
    response.delete_cookie(settings.session_cookie, path="/")
    return OkOut()


@router.get("/sessions", response_model=list[SessionOut], summary="我的活跃会话")
def list_sessions(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[SessionOut]:
    rows = db.scalars(
        select(Session)
        .where(Session.user_id == principal.user.id, Session.revoked_at.is_(None))
        .order_by(Session.last_seen_at.desc())
    ).all()
    return [SessionOut.model_validate(row) for row in rows]


@router.delete("/sessions/{session_id}", response_model=OkOut, summary="吊销一个会话")
def revoke_session(
    session_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(Session, session_id)
    if row is None or row.user_id != principal.user.id:
        raise Unauthorized("会话不存在")
    row.revoked_at = datetime.now(UTC)
    return OkOut()


@router.put("/password", response_model=OkOut, summary="修改口令（其他设备全部退出）")
def change_password(
    payload: PasswordChangeIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = principal.user
    if not verify_password(payload.current_password, user.password_hash):
        raise Unauthorized("当前口令不正确")
    if verify_password(payload.new_password, user.password_hash):
        raise Conflict("新口令不能与旧口令相同")
    user.password_hash = hash_password(payload.new_password)
    now = datetime.now(UTC)
    for row in db.scalars(
        select(Session).where(Session.user_id == user.id, Session.revoked_at.is_(None))
    ).all():
        if row.id != principal.session_id:
            row.revoked_at = now
    return OkOut()


register_stubs(
    router,
    [
        ("POST", "/registrations", "注册（发送邮箱验证码）"),
        ("POST", "/registrations/resend", "重发注册验证码"),
        ("POST", "/registrations/verifications", "校验邮箱验证码并激活账号"),
        ("GET", "/activations/{token}", "预览队友激活链接"),
        ("POST", "/activations", "使用激活链接设置口令并加入团队"),
        ("POST", "/password-reset-requests", "申请口令重置（发送验证码）"),
        ("POST", "/password-reset-grants", "用验证码换取重置授权"),
        ("POST", "/bot-challenges", "签发人机校验挑战"),
    ],
    tag="auth",
)

