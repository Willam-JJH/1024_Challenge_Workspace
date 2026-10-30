"""账号：登录、注册、激活、口令重置、会话。

流程设计（全部走一次性凭据）：

- 注册：``POST /auth/registrations`` 发验证码 →
  ``POST /auth/registrations/verifications`` 激活并登录
- 口令重置：``POST /auth/password-reset-requests`` → ``POST /auth/password-reset-grants``（换授权）
  → ``POST /auth/password-resets``（设新口令）
- 队友激活：``GET /auth/activations/{token}`` 预览 → ``POST /auth/activations`` 设口令并入队
"""

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends, Request, Response
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, Forbidden, NotFound, Unauthorized, Unprocessable
from app.core.net import client_ip
from app.core.security import hash_password, hash_token, new_token, verify_password
from app.models.event import Company, Event, Invitation
from app.models.identity import Session, User
from app.schemas.auth import (
    ActivationIn,
    BotChallengeOut,
    CodeSentOut,
    LoginIn,
    PasswordChangeIn,
    PasswordResetGrantIn,
    PasswordResetIn,
    PasswordResetRequestIn,
    RegisterIn,
    ResetGrantOut,
    SessionResponse,
    VerifyRegistrationIn,
)
from app.schemas.common import OkOut, SessionOut, UserOut
from app.schemas.company import ActivationPreviewOut
from app.services import botcheck, codes
from app.services.companies import add_member

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


def _sign_in(
    db: DbSession, user: User, request: Request, response: Response
) -> SessionResponse:
    _session, token = _create_session(db, user, request)
    _set_session_cookie(response, token)
    expires = datetime.now(UTC) + timedelta(days=settings.session_ttl_days)
    return SessionResponse(user=UserOut.model_validate(user), token=token, expires_at=expires)


# --- 会话 -------------------------------------------------------------------


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
    return _sign_in(db, user, request, response)


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


# --- 口令 -------------------------------------------------------------------


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


@router.post(
    "/password-reset-requests",
    response_model=CodeSentOut,
    summary="申请口令重置（发送验证码；不透露邮箱是否存在）",
)
def request_password_reset(
    payload: PasswordResetRequestIn, db: DbSession = Depends(get_db)
) -> CodeSentOut:
    user = db.scalar(select(User).where(User.email == payload.email.lower()))
    if user is not None:
        codes.issue_code(
            db, email=user.email, purpose="password_reset", locale=user.locale
        )
    return CodeSentOut(expires_in=codes.CODE_TTL_MINUTES * 60)


@router.post(
    "/password-reset-grants",
    response_model=ResetGrantOut,
    summary="用验证码换取一次性重置授权",
)
def grant_password_reset(
    payload: PasswordResetGrantIn, db: DbSession = Depends(get_db)
) -> ResetGrantOut:
    codes.consume_code(
        db, email=payload.email, purpose="password_reset", code=payload.code
    )
    token = codes.issue_grant(db, email=payload.email, purpose="reset_grant")
    return ResetGrantOut(token=token, expires_in=codes.GRANT_TTL_MINUTES * 60)


@router.post("/password-resets", response_model=OkOut, summary="用授权令牌设置新口令")
def reset_password(payload: PasswordResetIn, db: DbSession = Depends(get_db)) -> OkOut:
    grant = codes.consume_grant(db, token=payload.token, purpose="reset_grant")
    user = db.scalar(select(User).where(User.email == grant.email))
    if user is None:
        raise NotFound("账号不存在")
    user.password_hash = hash_password(payload.new_password)
    if user.status == "pending":
        user.status = "active"
    now = datetime.now(UTC)
    for row in db.scalars(
        select(Session).where(Session.user_id == user.id, Session.revoked_at.is_(None))
    ).all():
        row.revoked_at = now
    return OkOut()


# --- 注册 -------------------------------------------------------------------


@router.post(
    "/registrations",
    response_model=CodeSentOut,
    status_code=202,
    summary="注册（发送邮箱验证码）",
)
def register(
    payload: RegisterIn, db: DbSession = Depends(get_db)
) -> CodeSentOut:
    if settings.require_bot_check:
        if not payload.bot_challenge or not payload.bot_nonce:
            raise Unprocessable("缺少人机校验")
        botcheck.verify(payload.bot_challenge, payload.bot_nonce)

    email = payload.email.lower()
    user = db.scalar(select(User).where(User.email == email))
    if user is not None and user.status == "active":
        raise Conflict("该邮箱已注册，请直接登录")
    if user is None:
        user = User(
            email=email,
            password_hash=hash_password(payload.password),
            display_name=payload.display_name,
            locale=payload.locale,
            status="pending",
        )
        db.add(user)
    else:
        user.password_hash = hash_password(payload.password)
        user.display_name = payload.display_name
        user.locale = payload.locale
    db.flush()

    codes.issue_code(db, email=email, purpose="register", locale=payload.locale)
    return CodeSentOut(expires_in=codes.CODE_TTL_MINUTES * 60)


@router.post(
    "/registrations/resend", response_model=CodeSentOut, summary="重发注册验证码"
)
def resend_registration(
    payload: PasswordResetRequestIn, db: DbSession = Depends(get_db)
) -> CodeSentOut:
    user = db.scalar(select(User).where(User.email == payload.email.lower()))
    if user is not None and user.status == "pending":
        codes.issue_code(db, email=user.email, purpose="register", locale=user.locale)
    return CodeSentOut(expires_in=codes.CODE_TTL_MINUTES * 60)


@router.post(
    "/registrations/verifications",
    response_model=SessionResponse,
    summary="校验邮箱验证码：激活账号并登录",
)
def verify_registration(
    payload: VerifyRegistrationIn,
    request: Request,
    response: Response,
    db: DbSession = Depends(get_db),
) -> SessionResponse:
    user = db.scalar(select(User).where(User.email == payload.email.lower()))
    if user is None:
        raise NotFound("账号不存在")
    codes.consume_code(db, email=user.email, purpose="register", code=payload.code)
    user.status = "active"
    return _sign_in(db, user, request, response)


# --- 队友激活 ---------------------------------------------------------------


def _invitation_by_token(db: DbSession, token: str) -> Invitation:
    inv = db.scalar(
        select(Invitation).where(
            Invitation.token_hash == hash_token(token), Invitation.status == "pending"
        )
    )
    if inv is None:
        raise NotFound("邀请无效或已使用")
    if inv.expires_at is not None and inv.expires_at < datetime.now(UTC):
        raise Unprocessable("邀请已过期，请让队长重新发送")
    return inv


@router.get(
    "/activations/{token}",
    response_model=ActivationPreviewOut,
    summary="预览队友激活链接（无需登录）",
)
def preview_activation(token: str, db: DbSession = Depends(get_db)) -> ActivationPreviewOut:
    inv = _invitation_by_token(db, token)
    company = db.get(Company, inv.company_id)
    event = db.get(Event, company.event_id) if company else None
    if company is None or event is None:
        raise NotFound("邀请所属的公司或赛事不存在")
    return ActivationPreviewOut(
        email=inv.email, company_name=company.name, event_name=event.name
    )


@router.post(
    "/activations",
    response_model=SessionResponse,
    summary="使用激活链接设置口令并入队",
)
def activate(
    payload: ActivationIn,
    request: Request,
    response: Response,
    db: DbSession = Depends(get_db),
) -> SessionResponse:
    inv = _invitation_by_token(db, payload.token)
    company = db.get(Company, inv.company_id)
    if company is None:
        raise NotFound("公司不存在")

    user = db.scalar(select(User).where(User.email == inv.email))
    if user is not None and user.status == "active":
        raise Conflict("该邮箱已有账号，请登录后接受邀请")

    if user is None:
        user = User(
            email=inv.email,
            password_hash=hash_password(payload.password),
            display_name=payload.display_name or inv.email.split("@")[0],
            status="active",
        )
        db.add(user)
        db.flush()
    else:
        user.password_hash = hash_password(payload.password)
        user.status = "active"
        if payload.display_name:
            user.display_name = payload.display_name

    add_member(db, company, user)
    inv.status = "accepted"
    return _sign_in(db, user, request, response)


# --- 人机校验 ---------------------------------------------------------------


@router.post(
    "/bot-challenges", response_model=BotChallengeOut, summary="签发人机校验挑战"
)
def bot_challenge() -> BotChallengeOut:
    challenge, difficulty, expires_at = botcheck.issue_challenge()
    return BotChallengeOut(
        challenge=challenge, difficulty=difficulty, expires_at=expires_at
    )


__all__ = ["Forbidden", "router"]
