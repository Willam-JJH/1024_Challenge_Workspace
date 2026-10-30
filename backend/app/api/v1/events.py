"""赛事本体：当前赛事、日程、开关、公开统计。"""

from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.stubs import register_stubs
from app.models.event import Event
from app.schemas.common import EventOut

router = APIRouter(prefix="/events", tags=["events"])


@router.get("", response_model=list[EventOut], summary="赛事列表")
def list_events(db: DbSession = Depends(get_db)) -> list[EventOut]:
    rows = db.scalars(select(Event).order_by(Event.created_at.desc())).all()
    return [EventOut.model_validate(row) for row in rows]


@router.get("/current", response_model=EventOut, summary="当前赛事")
def current_event(db: DbSession = Depends(get_db)) -> EventOut:
    row = db.scalar(select(Event).where(Event.slug == settings.current_event_slug))
    if row is None:
        raise HTTPException(status_code=404, detail="当前赛事尚未配置")
    return EventOut.model_validate(row)


@router.get("/{event_id}", response_model=EventOut, summary="单个赛事")
def get_event(event_id: str, db: DbSession = Depends(get_db)) -> EventOut:
    row = db.get(Event, event_id)
    if row is None:
        raise HTTPException(status_code=404, detail="赛事不存在")
    return EventOut.model_validate(row)


register_stubs(
    router,
    [
        ("PATCH", "/{event_id}/schedule", "编辑各阶段时间（UTC 时刻，顺序校验）"),
        ("GET", "/{event_id}/switches", "赛事开关状态"),
        ("PUT", "/{event_id}/switches", "开关报名、锁定全部作品／全部名单"),
        ("GET", "/{event_id}/stats", "落地页聚合计数"),
        ("GET", "/{event_id}/announcements", "赛事公告"),
        ("GET", "/{event_id}/sponsors", "赞助商条"),
        ("GET", "/{event_id}/shortlist", "已公布的入围名单"),
        ("GET", "/{event_id}/results", "已公布的结果：奖项与最终榜"),
        ("GET", "/{event_id}/market", "实时投票榜"),
        ("GET", "/{event_id}/market/big-screen", "大屏仪式视图"),
        ("GET", "/{event_id}/market/stream", "投票榜 SSE 推送"),
        ("GET", "/{event_id}/pitch", "Pitching Day 直播页"),
        ("GET", "/{event_id}/pitch/stream", "直播页 SSE 推送"),
    ],
    tag="events",
)
