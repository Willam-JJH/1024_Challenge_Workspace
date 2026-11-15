"""应用配置。

所有环境变量以 ``APP_`` 为前缀，例如 ``APP_DATABASE_URL``。
"""

import json
from functools import lru_cache
from typing import Annotated

from pydantic import field_validator
from pydantic_settings import BaseSettings, NoDecode, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=(".env", "../.env"), env_prefix="APP_", extra="ignore"
    )

    app_name: str = "1024 Challenge Workspace API"
    api_prefix: str = "/api/v1"
    environment: str = "dev"
    debug: bool = False

    # 数据库：默认 SQLite 便于本地起步，生产使用 PostgreSQL
    database_url: str = "sqlite:///./dev.db"

    # 会话
    session_cookie: str = "sid"
    session_ttl_days: int = 14
    cookie_secure: bool = False
    cookie_domain: str | None = None

    # CORS（前端重设计后填写）
    cors_origins: Annotated[list[str], NoDecode] = ["http://localhost:5173"]

    # 赛事开关
    registration_open: bool = True
    current_event_slug: str = "1024-challenge"

    # 部署在 Cloudflare 之后：采信 CF-Connecting-IP（仅限受信代理）
    behind_cloudflare: bool = False
    trusted_proxies: Annotated[list[str], NoDecode] = []
    public_domain: str = "example.com"

    # 投票：每人最多投给几家公司
    wallet_max_votes: int = 3

    # 人机校验（自托管工作量证明）
    require_bot_check: bool = False
    bot_check_secret: str = "dev-only-change-me"

    # 限流（进程内滑动窗口；多副本部署时每个副本各算一份）
    rate_limit_enabled: bool = True
    # 全域每 IP 每秒请求上限；0 表示关闭（本机压测需要）
    rate_limit_rps: int = 0
    # 登录失败：同一 IP+邮箱每 15 分钟的次数
    rate_limit_login_pair: int = 10
    # 登录失败：同一 IP 每 15 分钟的次数
    rate_limit_login_ip: int = 60
    # 发验证码：同一邮箱每小时次数（注册与口令重置共用）
    rate_limit_code_email: int = 5
    # 发验证码：同一 IP 每小时次数（注册与口令重置共用）
    rate_limit_code_ip: int = 30
    # 发验证码：同一邮箱两次之间的最小间隔（秒）
    rate_limit_code_cooldown: int = 60
    # 校验验证码：同一邮箱每小时次数
    rate_limit_verify_email: int = 20

    # 上传
    upload_dir: str = "uploads"
    max_upload_bytes: int = 100 * 1024 * 1024

    # 邮件：console 只写日志；smtp 走真实投递
    mail_driver: str = "console"
    mail_from: str = ""
    mail_from_name: str = "1024 Challenge"
    smtp_host: str = ""
    smtp_port: int = 465
    smtp_user: str = ""
    smtp_password: str = ""
    # ssl（465，隐式 TLS）| starttls（587）| none（本地假服务）
    smtp_tls: str = "ssl"
    smtp_timeout: float = 10.0

    # 定时广播 worker 的轮询间隔（秒）；0 = 关闭
    broadcast_poll_seconds: int = 30

    # 分页
    default_page_size: int = 20
    max_page_size: int = 100

    @field_validator("cors_origins", "trusted_proxies", mode="before")
    @classmethod
    def _split_list(cls, value: object) -> object:
        """JSON 数组与逗号分隔都接受，与 Go 侧 envList 一致。"""
        if isinstance(value, str):
            text = value.strip()
            if text.startswith("["):
                return json.loads(text)
            return [item.strip() for item in text.split(",") if item.strip()]
        return value


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()


settings = get_settings()
