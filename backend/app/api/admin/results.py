"""管理台：入围名单与奖项公布。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.event import Company
from app.models.judging import Award, ShortlistEntry
from app.models.project import Project
from app.schemas.common import OkOut
from app.schemas.judging import AdvanceIn, AwardIn, AwardOut, ShortlistOut
from app.services import companies as company_service
from app.services import votes as vote_service

router = APIRouter(prefix="/admin/results", tags=["admin"])


@router.post("/shortlist", response_model=list[ShortlistOut], summary="按票数自动公布入围名单")
def announce_shortlist(
    payload: AdvanceIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[ShortlistOut]:
    event = company_service.current_event(db)
    published = db.execute(
        select(Project, Company)
        .join(Company, Company.id == Project.company_id)
        .where(Project.event_id == event.id, Project.status == "published")
    ).all()
    votes = vote_service.live_counts(db, event.id)

    ranked = sorted(
        published,
        key=lambda pair: (-votes.get(pair[0].company_id, 0), pair[1].name),
    )
    # 作品数不超过席位时全部入围，否则取前 N
    chosen = ranked if len(ranked) <= payload.top_n else ranked[: payload.top_n]

    for entry in db.scalars(
        select(ShortlistEntry).where(ShortlistEntry.event_id == event.id)
    ).all():
        db.delete(entry)
    db.flush()

    now = datetime.now(UTC)
    out = []
    for position, (project, company) in enumerate(chosen, start=1):
        db.add(
            ShortlistEntry(
                event_id=event.id,
                project_id=project.id,
                position=position,
                announced_at=now,
            )
        )
        out.append(
            ShortlistOut(
                project_id=project.id,
                project_title=project.title,
                company_name=company.name,
                position=position,
            )
        )
    return out


@router.delete("/shortlist", response_model=OkOut, summary="撤回入围名单")
def withdraw_shortlist(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> OkOut:
    event = company_service.current_event(db)
    for entry in db.scalars(
        select(ShortlistEntry).where(ShortlistEntry.event_id == event.id)
    ).all():
        db.delete(entry)
    return OkOut()


@router.post("/awards", response_model=AwardOut, status_code=201, summary="公布一个奖项")
def create_award(
    payload: AwardIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> AwardOut:
    event = company_service.current_event(db)
    project = db.get(Project, payload.project_id)
    if project is None or project.status != "published":
        raise NotFound("作品不存在或尚未发布")
    existing = db.scalar(
        select(Award).where(
            Award.event_id == event.id,
            Award.kind == payload.kind,
            Award.project_id == project.id,
        )
    )
    if existing is not None:
        raise Conflict("这个作品已经获得该奖项")

    award = Award(
        event_id=event.id,
        kind=payload.kind,
        title=payload.title,
        project_id=project.id,
        announced_at=datetime.now(UTC),
    )
    db.add(award)
    db.flush()
    company = db.get(Company, project.company_id)
    return AwardOut(
        id=award.id,
        kind=award.kind,
        title=award.title,
        project_id=project.id,
        project_title=project.title,
        company_name=company.name if company else "",
    )


@router.delete("/awards/{award_id}", response_model=OkOut, summary="撤回奖项")
def delete_award(
    award_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    award = db.get(Award, award_id)
    if award is None:
        raise NotFound("奖项不存在")
    db.delete(award)
    return OkOut()
