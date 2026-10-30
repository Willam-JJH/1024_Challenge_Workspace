"""公告、赞助商（公开读取）与系统级小接口。"""

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.stubs import register_stubs
from app.models.admin import Announcement, Sponsor
from app.schemas.admin import AnnouncementOut, SponsorOut
from app.services import companies as company_service

router = APIRouter(tags=["content"])


@router.get("/announcements", response_model=list[AnnouncementOut], summary="已发布的公告")
def list_announcements(db: DbSession = Depends(get_db)) -> list[AnnouncementOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Announcement)
        .where(Announcement.event_id == event.id, Announcement.status == "published")
        .order_by(Announcement.published_at.desc())
    ).all()
    return [AnnouncementOut.model_validate(row) for row in rows]


@router.get("/sponsors", response_model=list[SponsorOut], summary="赞助商条")
def list_sponsors(db: DbSession = Depends(get_db)) -> list[SponsorOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Sponsor)
        .where(Sponsor.event_id == event.id)
        .order_by(Sponsor.position, Sponsor.created_at)
    ).all()
    return [SponsorOut.model_validate(row) for row in rows]


register_stubs(
    router,
    [
        ("POST", "/csp-reports", "浏览器上报 CSP 违规（只记录）"),
        ("POST", "/link-previews", "生成链接卡片预览"),
        ("GET", "/github/repos/{owner}/{repo}", "GitHub 仓库信息卡（带缓存）"),
    ],
    tag="content",
)
