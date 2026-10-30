"""Pitching Day 直播页：开播状态、多区域流源、页面留言与 SSE。"""

import asyncio
import json

from fastapi import APIRouter, Depends
from fastapi.responses import StreamingResponse
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import SessionLocal, get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Forbidden, NotFound
from app.models.event import Event
from app.models.identity import User
from app.models.pitch import PitchMessage, PitchSource
from app.schemas.common import OkOut
from app.schemas.pitch import MessageIn, MessageOut, PitchStateOut, SourceOut
from app.services import companies as company_service

router = APIRouter(prefix="/pitch", tags=["pitch"])


def _sources(db: DbSession, event_id: str) -> list[PitchSource]:
    return list(
        db.scalars(
            select(PitchSource)
            .where(PitchSource.event_id == event_id, PitchSource.enabled.is_(True))
            .order_by(PitchSource.position, PitchSource.created_at)
        ).all()
    )


def _messages(
    db: DbSession, event_id: str, *, limit: int = 50, include_hidden: bool = False
) -> list[MessageOut]:
    stmt = (
        select(PitchMessage, User)
        .join(User, User.id == PitchMessage.author_id)
        .where(PitchMessage.event_id == event_id)
        .order_by(PitchMessage.created_at.desc())
        .limit(limit)
    )
    if not include_hidden:
        stmt = stmt.where(PitchMessage.status == "visible")
    rows = db.execute(stmt).all()
    return [
        MessageOut(
            id=message.id,
            author_id=message.author_id,
            author_name=user.display_name,
            body=message.body,
            created_at=message.created_at,
        )
        for message, user in rows
    ]


def _state(db: DbSession, event: Event) -> dict:
    return {
        "on_air": event.pitch_on_air,
        "title": event.pitch_title,
        "sources": [
            {
                "id": source.id,
                "kind": source.kind,
                "name": source.name,
                "url": source.url,
                "region": source.region,
                "enabled": source.enabled,
                "position": source.position,
            }
            for source in _sources(db, event.id)
        ],
        "messages": [
            {
                "id": message.id,
                "author_id": message.author_id,
                "author_name": message.author_name,
                "body": message.body,
                "created_at": message.created_at.isoformat(),
            }
            for message in reversed(_messages(db, event.id, limit=20))
        ],
    }


@router.get("", response_model=PitchStateOut, summary="直播页：是否在播、流源与留言")
def get_pitch(db: DbSession = Depends(get_db)) -> PitchStateOut:
    event = company_service.current_event(db)
    return PitchStateOut.model_validate(_state(db, event))


@router.get("/sources", response_model=list[SourceOut], summary="可用流源")
def get_sources(db: DbSession = Depends(get_db)) -> list[SourceOut]:
    event = company_service.current_event(db)
    return [SourceOut.model_validate(source) for source in _sources(db, event.id)]


@router.get("/messages", response_model=list[MessageOut], summary="直播页留言")
def get_messages(db: DbSession = Depends(get_db)) -> list[MessageOut]:
    event = company_service.current_event(db)
    return _messages(db, event.id)


@router.post("/messages", response_model=MessageOut, status_code=201, summary="在直播页留言")
def post_message(
    payload: MessageIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> MessageOut:
    event = company_service.current_event(db)
    row = PitchMessage(event_id=event.id, author_id=principal.user.id, body=payload.body)
    db.add(row)
    db.flush()
    return MessageOut(
        id=row.id,
        author_id=row.author_id,
        author_name=principal.user.display_name,
        body=row.body,
        created_at=row.created_at,
    )


@router.delete("/messages/{message_id}", response_model=OkOut, summary="撤回我自己的留言")
def delete_message(
    message_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(PitchMessage, message_id)
    if row is None:
        raise NotFound("留言不存在")
    if row.author_id != principal.user.id and principal.user.role not in {"staff", "admin"}:
        raise Forbidden("只能撤回自己的留言")
    row.status = "hidden"
    return OkOut()


@router.get("/stream", summary="直播页 SSE 推送")
async def stream_pitch() -> StreamingResponse:
    async def events():  # noqa: ANN202
        last: str | None = None
        idle = 0
        while True:
            with SessionLocal() as db:
                event = company_service.current_event(db)
                payload = _state(db, event)
            body = json.dumps(payload, ensure_ascii=False)
            if body != last:
                last = body
                yield f"event: pitch\ndata: {body}\n\n"
                idle = 0
            else:
                idle += 1
                if idle >= 5:
                    idle = 0
                    yield ": ping\n\n"
            await asyncio.sleep(3)

    return StreamingResponse(
        events(),
        media_type="text/event-stream",
        headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
    )
