"""健康检查。"""

from fastapi import APIRouter, Depends
from sqlalchemy import text
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.schemas.common import HealthOut

router = APIRouter(tags=["system"])


@router.get("/health", response_model=HealthOut, summary="服务健康检查")
def health(db: DbSession = Depends(get_db)) -> HealthOut:
    try:
        db.execute(text("SELECT 1"))
        database = "ok"
    except Exception:  # noqa: BLE001 - 健康检查不应抛出
        database = "unavailable"
    return HealthOut(status="ok", version="0.1.0", database=database)
