"""应用入口。"""

import logging
import uuid
from contextvars import ContextVar

from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

from app.api.llm import router as llm_router
from app.api.v1.oauth import well_known_router
from app.api.v1.router import api_router
from app.core.config import settings
from app.core.errors import install_error_handlers

request_id_ctx: ContextVar[str] = ContextVar("request_id", default="")

logging.basicConfig(
    level=logging.DEBUG if settings.debug else logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("challenge-api")


def create_app() -> FastAPI:
    app = FastAPI(
        title=settings.app_name,
        version="0.1.0",
        description="1024 Challenge 平台后端。接口规范见 docs/api/conventions.md。",
        docs_url="/docs" if settings.environment != "prod" else None,
        openapi_url="/openapi.json",
    )

    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.middleware("http")
    async def request_context(request: Request, call_next):  # noqa: ANN001, ANN202
        rid = request.headers.get("x-request-id") or uuid.uuid4().hex
        request.state.request_id = rid
        request_id_ctx.set(rid)
        response = await call_next(request)
        response.headers["X-Request-ID"] = rid
        return response

    install_error_handlers(app)
    app.include_router(api_router, prefix=settings.api_prefix)
    app.include_router(llm_router)
    app.include_router(well_known_router)
    return app


app = create_app()
