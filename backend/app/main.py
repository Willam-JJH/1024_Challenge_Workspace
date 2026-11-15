"""应用入口。"""

import asyncio
import logging
import uuid
from contextlib import asynccontextmanager, suppress
from contextvars import ContextVar

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

from app.api.llm import router as llm_router
from app.api.v1.oauth import well_known_router
from app.api.v1.router import api_router
from app.core.config import settings
from app.core.errors import RateLimited, error_response, install_error_handlers
from app.core.net import client_ip
from app.core.ratelimit import request_limit
from app.services import broadcast_worker

# 限流豁免：容器健康检查必须始终可达
RATE_LIMIT_EXEMPT = {"/api/v1/health"}

# 显式列出方法并按字典序排列，预检响应与 Go 侧逐字一致
CORS_METHODS = ["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]
CORS_MAX_AGE = 600

request_id_ctx: ContextVar[str] = ContextVar("request_id", default="")

logging.basicConfig(
    level=logging.DEBUG if settings.debug else logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("challenge-api")


@asynccontextmanager
async def lifespan(_app: FastAPI):  # noqa: ANN201
    """启动定时广播 worker；关停时取消。"""
    task = None
    if settings.broadcast_poll_seconds > 0:
        task = asyncio.create_task(broadcast_worker.run_forever())
    try:
        yield
    finally:
        if task is not None:
            task.cancel()
            with suppress(asyncio.CancelledError):
                await task


def create_app() -> FastAPI:
    app = FastAPI(
        title=settings.app_name,
        version="0.1.0",
        description="1024 Challenge 平台后端。接口规范见 docs/api/conventions.md。",
        docs_url="/docs" if settings.environment != "prod" else None,
        openapi_url="/openapi.json",
        lifespan=lifespan,
    )

    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_credentials=True,
        allow_methods=CORS_METHODS,
        allow_headers=["*"],
        max_age=CORS_MAX_AGE,
    )

    @app.middleware("http")
    async def request_context(request: Request, call_next):  # noqa: ANN001, ANN202
        rid = request.headers.get("x-request-id") or uuid.uuid4().hex
        request.state.request_id = rid
        request_id_ctx.set(rid)
        if request.url.path not in RATE_LIMIT_EXEMPT:
            try:
                request_limit(client_ip(request) or "")
            except RateLimited as exc:
                # 中间件里抛出的异常到不了 exception_handler，直接返回信封
                limited = error_response(request, exc)
                limited.headers["X-Request-ID"] = rid
                return limited
        response = await call_next(request)
        response.headers["X-Request-ID"] = rid
        return response

    install_error_handlers(app)
    app.include_router(api_router, prefix=settings.api_prefix)
    app.include_router(llm_router)
    app.include_router(well_known_router)
    return app


app = create_app()
