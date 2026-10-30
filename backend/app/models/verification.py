"""一次性验证码与授权令牌。

注册、口令重置、队友激活共用一张表：``purpose`` 区分用途，库中只存哈希。
"""

from datetime import datetime

from sqlalchemy import Integer, String
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class VerificationCode(BaseModel):
    __tablename__ = "verification_codes"

    email: Mapped[str] = mapped_column(String(320), index=True)
    # register | password_reset | reset_grant | activation
    purpose: Mapped[str] = mapped_column(String(32), index=True)
    code_hash: Mapped[str] = mapped_column(String(64), index=True)
    expires_at: Mapped[datetime] = mapped_column(UTCDateTime())
    consumed_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
    attempts: Mapped[int] = mapped_column(Integer, default=0)
