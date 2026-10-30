"""管理台：投票状态、选票明细与作废。"""

from fastapi import APIRouter, Depends, Query
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Unprocessable
from app.models.event import Company
from app.models.identity import User
from app.models.vote import Vote
from app.schemas.common import OkOut
from app.schemas.vote import BallotOut, BoardRow, VisibilityIn, VoidIn, VotingAdminOut
from app.services import audit
from app.services import companies as company_service
from app.services import votes as vote_service

router = APIRouter(prefix="/admin/voting", tags=["admin"])


@router.get("", response_model=VotingAdminOut, summary="投票状态与实时总数")
def get_voting(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> VotingAdminOut:
    event = company_service.current_event(db)
    board = [BoardRow(**row) for row in vote_service.board(db, event)]
    ballots = int(
        db.scalar(
            select(func.count()).select_from(Vote).where(Vote.event_id == event.id)
        )
        or 0
    )
    return VotingAdminOut(
        open=event.voting_open,
        visible=event.votes_visible,
        coins_total=sum(row.coins for row in board),
        ballots=ballots,
        board=board,
    )


def _ballots(
    db: DbSession, event_id: str, status: str | None
) -> list[BallotOut]:
    stmt = (
        select(Vote, User, Company)
        .join(User, User.id == Vote.voter_id)
        .join(Company, Company.id == Vote.company_id)
        .where(Vote.event_id == event_id)
        .order_by(Vote.created_at.desc())
        .limit(500)
    )
    if status:
        stmt = stmt.where(Vote.status == status)
    rows = db.execute(stmt).all()
    return [
        BallotOut(
            id=vote.id,
            voter_id=user.id,
            voter_name=user.display_name,
            company_id=company.id,
            company_name=company.name,
            status=vote.status,
            created_at=vote.created_at,
        )
        for vote, user, company in rows
    ]


@router.get("/ballots", response_model=list[BallotOut], summary="谁投给了谁（仅组织者）")
def list_ballots(
    status: str | None = Query(default=None, pattern="^(live|withdrawn|voided)$"),
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[BallotOut]:
    event = company_service.current_event(db)
    return _ballots(db, event.id, status)


@router.get("/events", response_model=list[BallotOut], summary="每一次投币、撤回与作废")
def list_events(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[BallotOut]:
    event = company_service.current_event(db)
    return _ballots(db, event.id, None)


@router.put("/visibility", response_model=OkOut, summary="对公众显示或隐藏实时总数")
def set_visibility(
    payload: VisibilityIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    event = company_service.current_event(db)
    event.votes_visible = payload.visible
    audit.record(
        db,
        actor_id=principal.user.id,
        action="voting.visibility",
        target_type="event",
        target_id=event.id,
        note=str(payload.visible),
    )
    return OkOut()


@router.post("/voids", response_model=OkOut, summary="作废一枚硬币或一位投票人")
def void_votes(
    payload: VoidIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if not payload.voter_id and not payload.company_id:
        raise Unprocessable("需要指定投票人或公司")
    event = company_service.current_event(db)
    stmt = select(Vote).where(Vote.event_id == event.id, Vote.status == "live")
    if payload.voter_id:
        stmt = stmt.where(Vote.voter_id == payload.voter_id)
    if payload.company_id:
        stmt = stmt.where(Vote.company_id == payload.company_id)
    voided = 0
    for vote in db.scalars(stmt).all():
        vote.status = "voided"
        voided += 1
    audit.record(
        db,
        actor_id=principal.user.id,
        action="voting.void",
        target_type="vote",
        target_id=payload.voter_id or payload.company_id,
        note=f"{voided} 枚",
    )
    return OkOut()
