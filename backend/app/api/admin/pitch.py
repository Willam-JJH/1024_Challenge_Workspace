"""管理台：直播页的开关、流源与留言审核。"""

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import NotFound
from app.models.identity import User
from app.models.pitch import PitchMessage, PitchSource
from app.schemas.common import OkOut
from app.schemas.pitch import (
    MessageOut,
    ModerationIn,
    MoveIn,
    PitchPatchIn,
    SourceIn,
    SourceOut,
    SourcePatchIn,
)
from app.services import companies as company_service

router = APIRouter(prefix="/admin/pitch", tags=["admin"])


@router.patch("", summary="直播标题与开播开关")
def patch_pitch(
    payload: PitchPatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> dict:
    event = company_service.current_event(db)
    if payload.title is not None:
        event.pitch_title = payload.title
    if payload.on_air is not None:
        event.pitch_on_air = payload.on_air
    db.flush()
    return {"title": event.pitch_title, "on_air": event.pitch_on_air}


@router.get("/sources", response_model=list[SourceOut], summary="全部流源")
def list_sources(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[SourceOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(PitchSource)
        .where(PitchSource.event_id == event.id)
        .order_by(PitchSource.position, PitchSource.created_at)
    ).all()
    return [SourceOut.model_validate(row) for row in rows]


@router.post("/sources", response_model=SourceOut, status_code=201, summary="新增流源")
def create_source(
    payload: SourceIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SourceOut:
    event = company_service.current_event(db)
    next_position = int(
        db.scalar(
            select(func.coalesce(func.max(PitchSource.position), 0)).where(
                PitchSource.event_id == event.id
            )
        )
        or 0
    ) + 1
    row = PitchSource(
        event_id=event.id,
        kind=payload.kind,
        name=payload.name,
        url=payload.url,
        region=payload.region,
        enabled=payload.enabled,
        position=next_position,
    )
    db.add(row)
    db.flush()
    return SourceOut.model_validate(row)


def _source(db: DbSession, source_id: str) -> PitchSource:
    row = db.get(PitchSource, source_id)
    if row is None:
        raise NotFound("流源不存在")
    return row


@router.patch("/sources/{source_id}", response_model=SourceOut, summary="修改流源")
def patch_source(
    source_id: str,
    payload: SourcePatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SourceOut:
    row = _source(db, source_id)
    for field in ("name", "url", "region", "enabled"):
        value = getattr(payload, field)
        if value is not None:
            setattr(row, field, value)
    db.flush()
    return SourceOut.model_validate(row)


@router.delete("/sources/{source_id}", response_model=OkOut, summary="删除流源")
def delete_source(
    source_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _source(db, source_id)
    db.delete(row)
    return OkOut()


@router.post("/sources/{source_id}/moves", response_model=OkOut, summary="上移／下移流源")
def move_source(
    source_id: str,
    payload: MoveIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _source(db, source_id)
    delta = -1 if payload.direction == "up" else 1
    neighbour = db.scalar(
        select(PitchSource).where(
            PitchSource.event_id == row.event_id,
            PitchSource.position == row.position + delta,
        )
    )
    if neighbour is not None:
        row.position, neighbour.position = neighbour.position, row.position
    return OkOut()


@router.get("/messages", response_model=list[MessageOut], summary="直播留言（含已隐藏）")
def list_messages(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[MessageOut]:
    event = company_service.current_event(db)
    rows = db.execute(
        select(PitchMessage, User)
        .join(User, User.id == PitchMessage.author_id)
        .where(PitchMessage.event_id == event.id)
        .order_by(PitchMessage.created_at.desc())
        .limit(200)
    ).all()
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


@router.post(
    "/messages/{message_id}/moderation", response_model=OkOut, summary="隐藏或恢复直播留言"
)
def moderate_message(
    message_id: str,
    payload: ModerationIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(PitchMessage, message_id)
    if row is None:
        raise NotFound("留言不存在")
    row.status = "hidden" if payload.hidden else "visible"
    return OkOut()
