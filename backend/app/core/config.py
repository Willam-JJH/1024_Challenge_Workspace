"""应用配置。

所有环境变量以 ``APP_`` 为前缀，例如 ``APP_DATABASE_URL``。
"""

from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


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
    cors_origins: list[str] = ["http://localhost:5173"]

    # 赛事开关
    registration_open: bool = True
    current_event_slug: str = "1024-challenge"

    # 部署在 Cloudflare 之后：采信 CF-Connecting-IP（仅限受信代理）
    behind_cloudflare: bool = False
    trusted_proxies: list[str] = []
    public_domain: str = "example.com"

    # 人机校验（自托管工作量证明）
    require_bot_check: bool = False
    bot_check_secret: str = "dev-only-change-me"

    # 分页
    default_page_size: int = 20
    max_page_size: int = 100


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()


settings = get_settings()
