"""游标分页。

列表统一返回 ``{"items": [...], "next_cursor": "..."}``；游标是 base64(id)。
"""

import base64
from typing import Any

from fastapi import Query
from pydantic import BaseModel

from app.core.config import settings
from app.core.errors import ApiError


class Page(BaseModel):
    items: list[Any]
    next_cursor: str | None = None


def encode_cursor(value: str) -> str:
    return base64.urlsafe_b64encode(value.encode()).decode().rstrip("=")


def decode_cursor(cursor: str) -> str:
    padding = "=" * (-len(cursor) % 4)
    try:
        return base64.urlsafe_b64decode(cursor + padding).decode()
    except Exception as exc:  # noqa: BLE001
        raise ApiError("游标无效", code="invalid_cursor", status_code=400) from exc


class PageParams(BaseModel):
    limit: int = settings.default_page_size
    cursor: str | None = None


def page_params(
    limit: int = Query(default=settings.default_page_size, ge=1, le=settings.max_page_size),
    cursor: str | None = Query(default=None),
) -> PageParams:
    return PageParams(limit=limit, cursor=cursor)
