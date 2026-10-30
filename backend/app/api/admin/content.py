"""管理台：功能开关、公告与赞助商。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.admin import Announcement, FeatureFlag, Sponsor
from app.schemas.admin import (
    AnnouncementIn,
    AnnouncementOut,
    FeatureFlagIn,
    FeatureFlagOut,
    FeatureFlagPatchIn,
    SponsorIn,
    SponsorOrderIn,
    SponsorOut,
)
from app.schemas.common import OkOut
from app.services import audit
from app.services import companies as company_service

router = APIRouter(prefix="/admin", tags=["admin"])


# --- 功能开关 ---------------------------------------------------------------


@router.get("/feature-flags", response_model=list[FeatureFlagOut], summary="功能开关")
def list_flags(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[FeatureFlagOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(FeatureFlag).where(FeatureFlag.event_id == event.id).order_by(FeatureFlag.key)
    ).all()
    return [FeatureFlagOut.model_validate(row) for row in rows]


@router.post(
    "/feature-flags", response_model=FeatureFlagOut, status_code=201, summary="新建功能开关"
)
def create_flag(
    payload: FeatureFlagIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> FeatureFlagOut:
    event = company_service.current_event(db)
    if db.scalar(
        select(FeatureFlag).where(
            FeatureFlag.event_id == event.id, FeatureFlag.key == payload.key
        )
    ):
        raise Conflict("同名开关已存在")
    row = FeatureFlag(
        event_id=event.id,
        key=payload.key,
        description=payload.description,
        enabled=payload.enabled,
    )
    db.add(row)
    db.flush()
    audit.record(
        db,
        actor_id=principal.user.id,
        action="flag.create",
        target_type="feature_flag",
        target_id=row.id,
        note=row.key,
    )
    return FeatureFlagOut.model_validate(row)


@router.patch(
    "/feature-flags/{flag_id}", response_model=FeatureFlagOut, summary="启用／停用或修改说明"
)
def patch_flag(
    flag_id: str,
    payload: FeatureFlagPatchIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> FeatureFlagOut:
    row = db.get(FeatureFlag, flag_id)
    if row is None:
        raise NotFound("开关不存在")
    if payload.description is not None:
        row.description = payload.description
    if payload.enabled is not None:
        row.enabled = payload.enabled
    db.flush()
    return FeatureFlagOut.model_validate(row)


@router.delete("/feature-flags/{flag_id}", response_model=OkOut, summary="删除功能开关")
def delete_flag(
    flag_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(FeatureFlag, flag_id)
    if row is None:
        raise NotFound("开关不存在")
    db.delete(row)
    return OkOut()


# --- 公告 -------------------------------------------------------------------


@router.get("/announcements", response_model=list[AnnouncementOut], summary="全部公告")
def list_announcements(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[AnnouncementOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Announcement)
        .where(Announcement.event_id == event.id)
        .order_by(Announcement.created_at.desc())
    ).all()
    return [AnnouncementOut.model_validate(row) for row in rows]


@router.post(
    "/announcements", response_model=AnnouncementOut, status_code=201, summary="写公告"
)
def create_announcement(
    payload: AnnouncementIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> AnnouncementOut:
    event = company_service.current_event(db)
    row = Announcement(
        event_id=event.id,
        title=payload.title,
        body=payload.body,
        status="published" if payload.publish else "draft",
        published_at=datetime.now(UTC) if payload.publish else None,
    )
    db.add(row)
    db.flush()
    return AnnouncementOut.model_validate(row)


@router.patch(
    "/announcements/{announcement_id}", response_model=AnnouncementOut, summary="编辑公告"
)
def patch_announcement(
    announcement_id: str,
    payload: AnnouncementIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> AnnouncementOut:
    row = db.get(Announcement, announcement_id)
    if row is None:
        raise NotFound("公告不存在")
    row.title = payload.title
    row.body = payload.body
    if payload.publish and row.status != "published":
        row.status = "published"
        row.published_at = datetime.now(UTC)
    db.flush()
    return AnnouncementOut.model_validate(row)


@router.delete("/announcements/{announcement_id}", response_model=OkOut, summary="删除公告")
def delete_announcement(
    announcement_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(Announcement, announcement_id)
    if row is None:
        raise NotFound("公告不存在")
    db.delete(row)
    return OkOut()


# --- 赞助商 -----------------------------------------------------------------


@router.get("/sponsors", response_model=list[SponsorOut], summary="赞助商条")
def list_sponsors(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[SponsorOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Sponsor)
        .where(Sponsor.event_id == event.id)
        .order_by(Sponsor.position, Sponsor.created_at)
    ).all()
    return [SponsorOut.model_validate(row) for row in rows]


@router.post("/sponsors", response_model=SponsorOut, status_code=201, summary="新增赞助商")
def create_sponsor(
    payload: SponsorIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SponsorOut:
    event = company_service.current_event(db)
    next_position = int(
        db.scalar(
            select(func.coalesce(func.max(Sponsor.position), 0)).where(
                Sponsor.event_id == event.id
            )
        )
        or 0
    ) + 1
    row = Sponsor(
        event_id=event.id,
        name=payload.name,
        logo_url=payload.logo_url,
        website=payload.website,
        position=next_position,
    )
    db.add(row)
    db.flush()
    return SponsorOut.model_validate(row)


@router.patch("/sponsors/{sponsor_id}", response_model=SponsorOut, summary="编辑赞助商")
def patch_sponsor(
    sponsor_id: str,
    payload: SponsorIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> SponsorOut:
    row = db.get(Sponsor, sponsor_id)
    if row is None:
        raise NotFound("赞助商不存在")
    row.name = payload.name
    row.logo_url = payload.logo_url
    row.website = payload.website
    db.flush()
    return SponsorOut.model_validate(row)


@router.delete("/sponsors/{sponsor_id}", response_model=OkOut, summary="移除赞助商")
def delete_sponsor(
    sponsor_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(Sponsor, sponsor_id)
    if row is None:
        raise NotFound("赞助商不存在")
    db.delete(row)
    return OkOut()


@router.put("/sponsors/order", response_model=OkOut, summary="赞助商排序")
def order_sponsors(
    payload: SponsorOrderIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Sponsor).where(Sponsor.event_id == event.id, Sponsor.id.in_(payload.ids))
    ).all()
    by_id = {row.id: row for row in rows}
    for position, sponsor_id in enumerate(payload.ids, start=1):
        row = by_id.get(sponsor_id)
        if row is not None:
            row.position = position
    return OkOut()
