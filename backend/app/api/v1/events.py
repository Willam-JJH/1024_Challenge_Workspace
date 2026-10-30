"""赛事本体：当前赛事、日程与公开统计。

原 `/events/{id}/market`、`/pitch`、`/shortlist` 等别名已删除：数据在
`/votes`、`/pitch`、`/results` 下已有正式路径，前端不要再依赖别名。
"""

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.errors import NotFound
from app.models.event import Company, Event
from app.models.exhibition import Comment, Visit
from app.models.project import Project
from app.models.vote import Vote
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
        raise NotFound("当前赛事尚未配置")
    return EventOut.model_validate(row)


@router.get("/{event_id}", response_model=EventOut, summary="单个赛事")
def get_event(event_id: str, db: DbSession = Depends(get_db)) -> EventOut:
    row = db.get(Event, event_id)
    if row is None:
        raise NotFound("赛事不存在")
    return EventOut.model_validate(row)


@router.get("/{event_id}/stats", summary="公开统计：人数、公司、作品、票")
def stats(event_id: str, db: DbSession = Depends(get_db)) -> dict:
    event = db.get(Event, event_id)
    if event is None:
        raise NotFound("赛事不存在")

    def count(model, *where) -> int:  # noqa: ANN001
        stmt = select(func.count()).select_from(model)
        if where:
            stmt = stmt.where(*where)
        return int(db.scalar(stmt) or 0)

    return {
        "companies": count(Company, Company.event_id == event.id),
        "projects_published": count(
            Project, Project.event_id == event.id, Project.status == "published"
        ),
        "votes": count(Vote, Vote.event_id == event.id, Vote.status == "live"),
        "comments": count(Comment, Comment.status == "visible"),
        "visits": count(Visit, Visit.event_id == event.id),
    }
