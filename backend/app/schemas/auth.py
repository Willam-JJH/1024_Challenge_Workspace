"""鉴权与账号生命周期。"""

from datetime import datetime

from pydantic import BaseModel, EmailStr, Field

from app.schemas.common import UserOut


class LoginIn(BaseModel):
    email: EmailStr
    password: str = Field(min_length=8, max_length=200)
    remember: bool = True


class SessionResponse(BaseModel):
    """登录成功后返回；会话令牌同时写入 ``sid`` cookie。"""

    user: UserOut
    token: str
    expires_at: datetime


class RegisterIn(BaseModel):
    email: EmailStr
    password: str = Field(min_length=8, max_length=200)
    display_name: str = Field(min_length=1, max_length=120)
    locale: str = Field(default="zh", max_length=8)
    bot_challenge: str | None = None
    bot_nonce: str | None = None


class VerifyRegistrationIn(BaseModel):
    email: EmailStr
    code: str = Field(min_length=4, max_length=12)


class PasswordChangeIn(BaseModel):
    current_password: str = Field(min_length=8, max_length=200)
    new_password: str = Field(min_length=8, max_length=200)


class PasswordResetRequestIn(BaseModel):
    email: EmailStr


class PasswordResetGrantIn(BaseModel):
    email: EmailStr
    code: str = Field(min_length=4, max_length=12)


class PasswordResetIn(BaseModel):
    token: str = Field(min_length=16)
    new_password: str = Field(min_length=8, max_length=200)


class ProfileUpdateIn(BaseModel):
    display_name: str | None = Field(default=None, min_length=1, max_length=120)
    locale: str | None = Field(default=None, max_length=8)


class CodeSentOut(BaseModel):
    status: str = "code_sent"
    expires_in: int


class ResetGrantOut(BaseModel):
    token: str
    expires_in: int


class BotChallengeOut(BaseModel):
    challenge: str
    difficulty: int
    expires_at: int


class ActivationIn(BaseModel):
    token: str = Field(min_length=16)
    password: str = Field(min_length=8, max_length=200)
    display_name: str | None = Field(default=None, max_length=120)


class ApiKeyIn(BaseModel):
    label: str = Field(min_length=1, max_length=80)
