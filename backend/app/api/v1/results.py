"""结果：已公布的奖项、入围名单与最终榜。"""

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.models.event import Company
from app.models.judging import Award, ShortlistEntry
from app.models.project import Project
from app.schemas.judging import AwardOut, ResultsOut, ShortlistOut
from app.schemas.vote import BoardRow
from app.services import companies as company_service
from app.services import votes as vote_service

router = APIRouter(prefix="/results", tags=["results"])


@router.get("", response_model=ResultsOut, summary="已公布的结果：奖项与最终榜")
def get_results(db: DbSession = Depends(get_db)) -> ResultsOut:
    event = company_service.current_event(db)
    rows = db.execute(
        select(Award, Project, Company)
        .join(Project, Project.id == Award.project_id)
        .join(Company, Company.id == Project.company_id)
        .where(Award.event_id == event.id, Award.announced_at.is_not(None))
        .order_by(Award.created_at)
    ).all()
    awards = [
        AwardOut(
            id=award.id,
            kind=award.kind,
            title=award.title,
            project_id=project.id,
            project_title=project.title,
            company_name=company.name,
        )
        for award, project, company in rows
    ]
    board = [BoardRow(**row) for row in vote_service.board(db, event)]
    return ResultsOut(announced=bool(awards), awards=awards, board=board)


@router.get("/shortlist", response_model=list[ShortlistOut], summary="已公布的入围名单")
def get_shortlist(db: DbSession = Depends(get_db)) -> list[ShortlistOut]:
    event = company_service.current_event(db)
    rows = db.execute(
        select(ShortlistEntry, Project, Company)
        .join(Project, Project.id == ShortlistEntry.project_id)
        .join(Company, Company.id == Project.company_id)
        .where(
            ShortlistEntry.event_id == event.id,
            ShortlistEntry.announced_at.is_not(None),
        )
        .order_by(ShortlistEntry.position)
    ).all()
    return [
        ShortlistOut(
            project_id=project.id,
            project_title=project.title,
            company_name=company.name,
            position=entry.position,
        )
        for entry, project, company in rows
    ]
