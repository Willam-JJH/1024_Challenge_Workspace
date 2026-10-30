"""OAuth 2.1 授权服务器（授权码 + PKCE）。"""

from datetime import datetime

from sqlalchemy import Boolean, ForeignKey, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel
from app.models.types import UTCDateTime


class OAuthClient(BaseModel):
    __tablename__ = "oauth_clients"

    name: Mapped[str] = mapped_column(String(120))
    client_id: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    # JSON 数组字符串
    redirect_uris: Mapped[str] = mapped_column(Text, default="[]")
    scopes: Mapped[str] = mapped_column(String(200), default="profile llm")
    is_public: Mapped[bool] = mapped_column(Boolean, default=True)


class OAuthConsent(BaseModel):
    __tablename__ = "oauth_consents"
    __table_args__ = (UniqueConstraint("user_id", "client_id", name="uq_consent"),)

    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    client_id: Mapped[str] = mapped_column(
        ForeignKey("oauth_clients.client_id", ondelete="CASCADE"), index=True
    )
    scope: Mapped[str] = mapped_column(String(200), default="profile")
    revoked_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class AuthorizationCode(BaseModel):
    __tablename__ = "oauth_codes"

    client_id: Mapped[str] = mapped_column(
        ForeignKey("oauth_clients.client_id", ondelete="CASCADE"), index=True
    )
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    redirect_uri: Mapped[str] = mapped_column(String(500))
    code_hash: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    code_challenge: Mapped[str] = mapped_column(String(128))
    scope: Mapped[str] = mapped_column(String(200), default="profile")
    expires_at: Mapped[datetime] = mapped_column(UTCDateTime())
    consumed_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)


class OAuthToken(BaseModel):
    __tablename__ = "oauth_tokens"

    client_id: Mapped[str] = mapped_column(
        ForeignKey("oauth_clients.client_id", ondelete="CASCADE"), index=True
    )
    user_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    device_id: Mapped[str] = mapped_column(String(64), index=True)
    access_hash: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    refresh_hash: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    scope: Mapped[str] = mapped_column(String(200), default="profile")
    expires_at: Mapped[datetime] = mapped_column(UTCDateTime())
    revoked_at: Mapped[datetime | None] = mapped_column(UTCDateTime(), nullable=True)
