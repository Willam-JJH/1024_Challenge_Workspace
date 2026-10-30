"""人机校验：自托管的工作量证明。

服务端签发有 HMAC 签名的挑战（无状态），客户端找到一个 nonce 使
``sha256(challenge + nonce)`` 的前 N 位为 0。难度默认为 18 位。
"""

import base64
import hashlib
import hmac
import secrets
import time

from app.core.config import settings
from app.core.errors import Unprocessable

DEFAULT_DIFFICULTY = 18
TTL_SECONDS = 600


def _sign(payload: str) -> str:
    secret = settings.bot_check_secret.encode()
    return hmac.new(secret, payload.encode(), hashlib.sha256).hexdigest()


def issue_challenge(difficulty: int = DEFAULT_DIFFICULTY) -> tuple[str, int, int]:
    """返回 (challenge, difficulty, expires_at 时间戳)。"""
    nonce = secrets.token_urlsafe(16)
    expires_at = int(time.time()) + TTL_SECONDS
    payload = f"{nonce}.{difficulty}.{expires_at}"
    signed = base64.urlsafe_b64encode(f"{payload}.{_sign(payload)}".encode()).decode()
    return signed, difficulty, expires_at


def _leading_zero_bits(digest: bytes) -> int:
    bits = 0
    for byte in digest:
        if byte == 0:
            bits += 8
            continue
        bits += 8 - byte.bit_length()
        break
    return bits


def verify(challenge: str, nonce: str) -> None:
    """校验挑战签名与工作量；不通过抛 422。"""
    try:
        raw = base64.urlsafe_b64decode(challenge.encode()).decode()
        nonce_part, difficulty_s, expires_s, signature = raw.rsplit(".", 3)
    except Exception as exc:  # noqa: BLE001
        raise Unprocessable("人机校验格式不正确") from exc

    payload = f"{nonce_part}.{difficulty_s}.{expires_s}"
    if not hmac.compare_digest(signature, _sign(payload)):
        raise Unprocessable("人机校验无效")
    if int(expires_s) < int(time.time()):
        raise Unprocessable("人机校验已过期，请重试")

    digest = hashlib.sha256(f"{challenge}{nonce}".encode()).digest()
    if _leading_zero_bits(digest) < int(difficulty_s):
        raise Unprocessable("人机校验未通过")
