"""队友集市与邀请链接。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, NotFound, Unprocessable
from app.core.security import hash_token
from app.models.event import Company, Event, Invitation, InviteLink
from app.models.identity import User
from app.schemas.common import CompanyOut, OkOut, UserLiteOut
from app.schemas.company import (
    CompanySummaryOut,
    InvitationPreviewIn,
    InvitationPreviewOut,
)
from app.services import companies as svc
from app.services.companies import add_member

router = APIRouter(tags=["finder"])


@router.get(
    "/finder/companies",
    response_model=list[CompanySummaryOut],
    summary="队友集市：还在招人的公司",
)
def finder_companies(db: DbSession = Depends(get_db)) -> list[CompanySummaryOut]:
    event = svc.current_event(db)
    out = []
    for company in db.scalars(
        select(Company).where(Company.event_id == event.id).order_by(Company.created_at)
    ).all():
        left = svc.seats_left(db, company, event)
        if left <= 0 or company.roster_locked:
            continue
        out.append(
            CompanySummaryOut(
                **CompanyOut.model_validate(company).model_dump(),
                member_count=event.company_seats - left,
                seats_left=left,
            )
        )
    return out


@router.get("/finder/people", response_model=list[UserLiteOut], summary="队友集市：还在找队的个人")
def finder_people(db: DbSession = Depends(get_db)) -> list[UserLiteOut]:
    event = svc.current_event(db)
    member_ids = select(svc.Membership.user_id).join(
        Company, Company.id == svc.Membership.company_id
    ).where(Company.event_id == event.id)
    rows = db.scalars(
        select(User)
        .where(User.status == "active", User.id.not_in(member_ids))
        .order_by(User.created_at)
        .limit(100)
    ).all()
    return [UserLiteOut(id=row.id, display_name=row.display_name) for row in rows]


def _preview(db: DbSession, token: str) -> InvitationPreviewOut:
    invitation = db.scalar(select(Invitation).where(Invitation.token_hash == hash_token(token)))
    if invitation is not None:
        company = db.get(Company, invitation.company_id)
        event = db.get(Event, company.event_id) if company else None
        if company is None or event is None:
            raise NotFound("邀请所属的公司或赛事不存在")
        return InvitationPreviewOut(
            kind="invitation",
            email=invitation.email,
            company_name=company.name,
            event_name=event.name,
        )

    link = db.scalar(select(InviteLink).where(InviteLink.token_hash == hash_token(token)))
    if link is not None and link.disabled_at is None:
        company = db.get(Company, link.company_id)
        event = db.get(Event, company.event_id) if company else None
        if company is None or event is None:
            raise NotFound("邀请链接所属的公司或赛事不存在")
        return InvitationPreviewOut(
            kind="link", email=None, company_name=company.name, event_name=event.name
        )

    raise NotFound("邀请无效或已失效")


@router.post(
    "/invitations/previews",
    response_model=InvitationPreviewOut,
    summary="预览邀请（无需登录）",
)
def preview_invitation(
    payload: InvitationPreviewIn, db: DbSession = Depends(get_db)
) -> InvitationPreviewOut:
    return _preview(db, payload.token)


@router.post(
    "/invitations/{invitation_id}/acceptance",
    response_model=OkOut,
    summary="接受邮件邀请（登录后）",
)
def accept_invitation(
    invitation_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    inv = db.get(Invitation, invitation_id)
    if inv is None or inv.status != "pending":
        raise NotFound("邀请不存在或已处理")
    if inv.email != principal.user.email.lower():
        raise NotFound("邀请不属于这个账号")
    if inv.expires_at is not None and inv.expires_at < datetime.now(UTC):
        raise Unprocessable("邀请已过期")
    company = db.get(Company, inv.company_id)
    if company is None:
        raise NotFound("公司不存在")
    add_member(db, company, principal.user)
    inv.status = "accepted"
    return OkOut()


@router.post(
    "/invitations/{invitation_id}/decline",
    response_model=OkOut,
    summary="拒绝邮件邀请",
)
def decline_invitation(
    invitation_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    inv = db.get(Invitation, invitation_id)
    if inv is None or inv.status != "pending":
        raise NotFound("邀请不存在或已处理")
    if inv.email != principal.user.email.lower():
        raise NotFound("邀请不属于这个账号")
    inv.status = "declined"
    return OkOut()


@router.post(
    "/invite-links/{token}/acceptance",
    response_model=OkOut,
    summary="通过邀请链接加入公司",
)
def accept_invite_link(
    token: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    link = db.scalar(select(InviteLink).where(InviteLink.token_hash == hash_token(token)))
    if link is None or link.disabled_at is not None:
        raise NotFound("邀请链接无效或已停用")
    company = db.get(Company, link.company_id)
    if company is None:
        raise NotFound("公司不存在")
    if company.roster_locked:
        raise Conflict("名单已锁定")
    add_member(db, company, principal.user)
    return OkOut()
