"""统一错误信封。

所有错误响应形如::

    {"error": {"code": "...", "message": "...", "details": {...}, "request_id": "..."}}
"""

from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException as StarletteHTTPException


class ApiError(Exception):
    status_code = 400
    code = "bad_request"

    def __init__(
        self,
        message: str,
        *,
        code: str | None = None,
        status_code: int | None = None,
        details: dict[str, Any] | None = None,
        headers: dict[str, str] | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        if code is not None:
            self.code = code
        if status_code is not None:
            self.status_code = status_code
        self.details = details or {}
        self.headers = headers or {}


class NotFound(ApiError):
    status_code, code = 404, "not_found"


class Unauthorized(ApiError):
    status_code, code = 401, "unauthorized"


class Forbidden(ApiError):
    status_code, code = 403, "forbidden"


class Conflict(ApiError):
    status_code, code = 409, "conflict"


class Unprocessable(ApiError):
    status_code, code = 422, "unprocessable"


class RateLimited(ApiError):
    """请求过于频繁。带上 Retry-After，客户端可以据此退避。"""

    status_code, code = 429, "rate_limited"

    def __init__(self, retry_after: int = 60, message: str = "请求过于频繁，请稍后再试") -> None:
        super().__init__(
            message,
            details={"retry_after": retry_after},
            headers={"Retry-After": str(max(retry_after, 1))},
        )
        self.retry_after = retry_after


class NotImplementedYet(ApiError):
    """接口契约已就位、实现尚未迁移。"""

    status_code, code = 501, "not_implemented"


def _payload(request: Request, code: str, message: str, details: dict[str, Any]) -> dict[str, Any]:
    return {
        "error": {
            "code": code,
            "message": message,
            "details": details,
            "request_id": getattr(request.state, "request_id", None),
        }
    }


_STATUS_CODES = {
    400: "bad_request",
    401: "unauthorized",
    403: "forbidden",
    404: "not_found",
    405: "method_not_allowed",
    409: "conflict",
    422: "unprocessable",
    429: "rate_limited",
}


def error_response(request: Request, exc: ApiError) -> JSONResponse:
    """把 ApiError 渲染成统一信封；中间件里也能直接用。"""
    return JSONResponse(
        _payload(request, exc.code, exc.message, exc.details),
        status_code=exc.status_code,
        headers=exc.headers or None,
    )


def install_error_handlers(app: FastAPI) -> None:
    @app.exception_handler(StarletteHTTPException)
    async def _http_error(request: Request, exc: StarletteHTTPException) -> JSONResponse:
        code = _STATUS_CODES.get(exc.status_code, "http_error")
        return JSONResponse(
            _payload(request, code, str(exc.detail), {}), status_code=exc.status_code
        )

    @app.exception_handler(ApiError)
    async def _api_error(request: Request, exc: ApiError) -> JSONResponse:
        return error_response(request, exc)

    @app.exception_handler(Exception)
    async def _unhandled(request: Request, exc: Exception) -> JSONResponse:
        return JSONResponse(
            _payload(request, "internal_error", "服务器内部错误", {}), status_code=500
        )
