"""评审：待评审作品与打分卡。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import NotFound
from app.models.event import Company
from app.models.judging import Score
from app.models.project import Project
from app.schemas.common import ProjectOut
from app.schemas.judging import JudgingProjectOut, ScoreIn, ScoreOut

router = APIRouter(prefix="/judging", tags=["judging"])


@router.get("/projects", response_model=list[JudgingProjectOut], summary="待我评审的作品")
def list_projects(
    principal: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[JudgingProjectOut]:
    rows = db.execute(
        select(Project, Company)
        .join(Company, Company.id == Project.company_id)
        .where(Project.status == "published")
        .order_by(Project.updated_at.desc())
    ).all()
    scores = {
        score.project_id: score
        for score in db.scalars(
            select(Score).where(Score.judge_id == principal.user.id)
        ).all()
    }
    out = []
    for project, company in rows:
        score = scores.get(project.id)
        out.append(
            JudgingProjectOut(
                project=ProjectOut.model_validate(project),
                company_name=company.name,
                my_score=(
                    ScoreOut(
                        project_id=score.project_id,
                        judge_id=score.judge_id,
                        creativity=score.creativity,
                        engineering=score.engineering,
                        presentation=score.presentation,
                        notes=score.notes,
                        submitted_at=score.submitted_at,
                    )
                    if score is not None
                    else None
                ),
            )
        )
    return out


@router.put(
    "/projects/{project_id}/scores", response_model=ScoreOut, summary="保存评分（1–10）"
)
def save_score(
    project_id: str,
    payload: ScoreIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> ScoreOut:
    project = db.get(Project, project_id)
    if project is None or project.status != "published":
        raise NotFound("作品不存在或尚未发布")

    score = db.scalar(
        select(Score).where(
            Score.project_id == project.id, Score.judge_id == principal.user.id
        )
    )
    if score is None:
        score = Score(project_id=project.id, judge_id=principal.user.id)
        db.add(score)
    score.creativity = payload.creativity
    score.engineering = payload.engineering
    score.presentation = payload.presentation
    score.notes = payload.notes
    if payload.submit:
        score.submitted_at = datetime.now(UTC)
    db.flush()
    return ScoreOut(
        project_id=score.project_id,
        judge_id=score.judge_id,
        creativity=score.creativity,
        engineering=score.engineering,
        presentation=score.presentation,
        notes=score.notes,
        submitted_at=score.submitted_at,
    )
