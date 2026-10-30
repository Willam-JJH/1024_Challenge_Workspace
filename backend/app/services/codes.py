"""验证码与重置授权：签发、校验、消费。"""

import secrets
from datetime import UTC, datetime, timedelta

from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.errors import Unprocessable
from app.core.security import hash_token
from app.models.verification import VerificationCode
from app.services.mailer import get_mailer

CODE_TTL_MINUTES = 15
GRANT_TTL_MINUTES = 30
MAX_ATTEMPTS = 5


def _issue(
    db: DbSession, *, email: str, purpose: str, raw: str, ttl_minutes: int
) -> VerificationCode:
    row = VerificationCode(
        email=email.lower(),
        purpose=purpose,
        code_hash=hash_token(raw),
        expires_at=datetime.now(UTC) + timedelta(minutes=ttl_minutes),
    )
    db.add(row)
    db.flush()
    return row


def issue_code(
    db: DbSession, *, email: str, purpose: str, locale: str = "zh"
) -> str:
    """签发 6 位数字验证码并投递。"""
    code = f"{secrets.randbelow(1_000_000):06d}"
    _issue(db, email=email, purpose=purpose, raw=code, ttl_minutes=CODE_TTL_MINUTES)
    get_mailer().send_code(to=email, code=code, purpose=purpose, locale=locale)
    return code


def consume_code(db: DbSession, *, email: str, purpose: str, code: str) -> VerificationCode:
    """校验并消费验证码；失败抛出 422。"""
    row = db.scalar(
        select(VerificationCode)
        .where(
            VerificationCode.email == email.lower(),
            VerificationCode.purpose == purpose,
            VerificationCode.consumed_at.is_(None),
        )
        .order_by(VerificationCode.created_at.desc())
    )
    if row is None:
        raise Unprocessable("没有待使用的验证码")
    if row.expires_at < datetime.now(UTC):
        raise Unprocessable("验证码已过期，请重新获取")
    if row.attempts >= MAX_ATTEMPTS:
        raise Unprocessable("尝试次数过多，请重新获取验证码")

    row.attempts += 1
    if not secrets.compare_digest(row.code_hash, hash_token(code)):
        raise Unprocessable("验证码不正确")

    row.consumed_at = datetime.now(UTC)
    return row


def issue_grant(db: DbSession, *, email: str, purpose: str) -> str:
    """签发一次性授权令牌（例如口令重置），返回明文，库中只留哈希。"""
    raw = secrets.token_urlsafe(32)
    _issue(db, email=email, purpose=purpose, raw=raw, ttl_minutes=GRANT_TTL_MINUTES)
    return raw


def consume_grant(db: DbSession, *, token: str, purpose: str) -> VerificationCode:
    row = db.scalar(
        select(VerificationCode).where(
            VerificationCode.purpose == purpose,
            VerificationCode.code_hash == hash_token(token),
            VerificationCode.consumed_at.is_(None),
        )
    )
    if row is None:
        raise Unprocessable("授权无效或已使用")
    if row.expires_at < datetime.now(UTC):
        raise Unprocessable("授权已过期，请重新申请")
    row.consumed_at = datetime.now(UTC)
    return row
