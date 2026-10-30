"""公司（参赛队）：组建、名单、邀请、入队申请与邀请链接。"""

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, Forbidden, NotFound
from app.core.security import hash_token, new_token
from app.models.event import Company, Event, Invitation, InviteLink, JoinRequest, Membership
from app.models.identity import User
from app.schemas.common import CompanyOut, OkOut
from app.schemas.company import (
    CompanyCreateIn,
    CompanyDetailOut,
    CompanySummaryOut,
    CompanyUpdateIn,
    InvitationCreatedOut,
    InvitationOut,
    InviteLinkOut,
    InviteMemberIn,
    JoinRequestIn,
    JoinRequestOut,
    MemberOut,
    RosterLockIn,
    TransferIn,
)
from app.services import companies as svc
from app.services.companies import add_member
from app.services.mailer import get_mailer

router = APIRouter(prefix="/companies", tags=["companies"])

INVITATION_TTL_DAYS = 14


def _members(db: DbSession, company_id: str) -> list[MemberOut]:
    rows = db.execute(
        select(Membership, User)
        .join(User, User.id == Membership.user_id)
        .where(Membership.company_id == company_id)
        .order_by(Membership.created_at)
    ).all()
    return [
        MemberOut(user_id=user.id, display_name=user.display_name, role=member.role)
        for member, user in rows
    ]


def _detail(db: DbSession, company: Company) -> CompanyDetailOut:
    event = db.get(Event, company.event_id)
    return CompanyDetailOut(
        **CompanyOut.model_validate(company).model_dump(),
        members=_members(db, company.id),
        seats_left=svc.seats_left(db, company, event) if event else 0,
    )


def _unique_slug(db: DbSession, event_id: str, name: str) -> str:
    base = svc.slugify(name)
    slug, n = base, 1
    while db.scalar(
        select(func.count())
        .select_from(Company)
        .where(Company.event_id == event_id, Company.slug == slug)
    ):
        n += 1
        slug = f"{base}-{n}"
    return slug


@router.post(
    "", response_model=CompanyDetailOut, status_code=201, summary="创建公司（创建者即队长）"
)
def create_company(
    payload: CompanyCreateIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> CompanyDetailOut:
    event = svc.current_event(db)
    if not event.registration_open:
        raise Forbidden("报名已关闭")
    if svc.my_membership(db, principal.user.id) is not None:
        raise Conflict("你已经在公司中")

    company = Company(
        event_id=event.id,
        slug=_unique_slug(db, event.id, payload.name),
        name=payload.name,
        blurb=payload.blurb,
        captain_id=principal.user.id,
    )
    db.add(company)
    db.flush()
    db.add(Membership(company_id=company.id, user_id=principal.user.id, role="captain"))
    principal.user.role = "captain"
    db.flush()
    return _detail(db, company)


@router.get("", response_model=list[CompanySummaryOut], summary="招募中的公司")
def list_companies(db: DbSession = Depends(get_db)) -> list[CompanySummaryOut]:
    event = svc.current_event(db)
    companies = db.scalars(
        select(Company).where(Company.event_id == event.id).order_by(Company.created_at)
    ).all()
    out = []
    for company in companies:
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


@router.get("/{company}", response_model=CompanyDetailOut, summary="公司主页（按 id 或 slug）")
def get_company(company: str, db: DbSession = Depends(get_db)) -> CompanyDetailOut:
    return _detail(db, svc.company_by_ref(db, company))


@router.patch("/{company_id}", response_model=CompanyDetailOut, summary="编辑公司资料（任意成员）")
def update_company(
    company_id: str,
    payload: CompanyUpdateIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> CompanyDetailOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_member(db, company.id, principal.user.id)
    if payload.name is not None:
        company.name = payload.name
    if payload.blurb is not None:
        company.blurb = payload.blurb
    db.flush()
    return _detail(db, company)


@router.get("/{company_id}/members", response_model=list[MemberOut], summary="成员名单")
def list_members(company_id: str, db: DbSession = Depends(get_db)) -> list[MemberOut]:
    company = svc.company_by_ref(db, company_id)
    return _members(db, company.id)


@router.delete("/{company_id}/members/{user_id}", response_model=OkOut, summary="移除成员（队长）")
def remove_member(
    company_id: str,
    user_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    if company.roster_locked:
        raise Conflict("名单已锁定")
    if user_id == principal.user.id:
        raise Conflict("队长不能用这个接口移除自己，请先移交队长")
    row = svc.membership_of(db, company.id, user_id)
    if row is None:
        raise NotFound("该成员不在公司中")
    db.delete(row)
    user = db.get(User, user_id)
    if user is not None:
        user.role = "participant"
    return OkOut()


@router.post("/{company_id}/leavings", response_model=OkOut, summary="退出公司")
def leave_company(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    row = svc.require_member(db, company.id, principal.user.id)
    if company.roster_locked:
        raise Conflict("名单已锁定")
    if row.role == "captain" and svc.seats_taken(db, company.id) > 1:
        raise Conflict("请先移交队长再退出")
    db.delete(row)
    principal.user.role = "participant"
    return OkOut()


@router.post("/{company_id}/captain-transfers", response_model=OkOut, summary="移交队长")
def transfer_captain(
    company_id: str,
    payload: TransferIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    mine = svc.require_captain(db, company.id, principal.user.id)
    target = svc.membership_of(db, company.id, payload.user_id)
    if target is None:
        raise NotFound("对方不是公司成员")
    mine.role = "member"
    target.role = "captain"
    company.captain_id = target.user_id
    principal.user.role = "member"
    target_user = db.get(User, target.user_id)
    if target_user is not None:
        target_user.role = "captain"
    return OkOut()


@router.post("/{company_id}/submitter-appointments", response_model=OkOut, summary="指定作品提交人")
def appoint_submitter(
    company_id: str,
    payload: TransferIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    target = svc.membership_of(db, company.id, payload.user_id)
    if target is None:
        raise NotFound("对方不是公司成员")
    for row in db.scalars(
        select(Membership).where(
            Membership.company_id == company.id, Membership.role == "submitter"
        )
    ).all():
        row.role = "member"
    target.role = "submitter"
    return OkOut()


@router.put("/{company_id}/roster-lock", response_model=OkOut, summary="锁定／解锁名单")
def set_roster_lock(
    company_id: str,
    payload: RosterLockIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    if principal.user.role not in {"staff", "admin"}:
        svc.require_captain(db, company.id, principal.user.id)
    company.roster_locked = payload.locked
    return OkOut()


# --- 邀请 -------------------------------------------------------------------


@router.get(
    "/{company_id}/invitations",
    response_model=list[InvitationOut],
    summary="待处理邀请（队长）",
)
def list_invitations(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> list[InvitationOut]:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    rows = db.scalars(
        select(Invitation)
        .where(Invitation.company_id == company.id, Invitation.status == "pending")
        .order_by(Invitation.created_at.desc())
    ).all()
    return [InvitationOut.model_validate(row) for row in rows]


def _deliver(db: DbSession, company: Company, invitation: Invitation) -> str:
    token = new_token()
    invitation.token_hash = hash_token(token)
    invitation.expires_at = datetime.now(UTC) + timedelta(days=INVITATION_TTL_DAYS)
    db.flush()
    get_mailer().send_notice(
        to=invitation.email,
        subject=f"邀请你加入 {company.name}",
        body=f"用这个链接激活并加入：/auth/activations/{token}",
    )
    return token


@router.post(
    "/{company_id}/invitations",
    response_model=InvitationCreatedOut,
    status_code=201,
    summary="邀请队友（队长）",
)
def invite_member(
    company_id: str,
    payload: InviteMemberIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> InvitationCreatedOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    if company.roster_locked:
        raise Conflict("名单已锁定")

    email = payload.email.lower()
    user = db.scalar(select(User).where(User.email == email))
    if user is not None:
        if svc.membership_of(db, company.id, user.id) is not None:
            raise Conflict("该账号已在公司中")
        if svc.my_membership(db, user.id) is not None:
            raise Conflict("该账号已在其他公司中")
    if svc.pending_invitation(db, company.id, email) is not None:
        raise Conflict("已有待接受的邀请")

    event = db.get(Event, company.event_id)
    if event is not None and svc.seats_left(db, company, event) <= 0:
        raise Conflict("公司人数已满")

    inv = Invitation(
        company_id=company.id,
        email=email,
        token_hash="",
        invited_by=principal.user.id,
    )
    db.add(inv)
    db.flush()
    token = _deliver(db, company, inv)
    return InvitationCreatedOut(
        invitation=InvitationOut.model_validate(inv),
        token=token if settings.environment != "prod" else "",
    )


@router.delete(
    "/{company_id}/invitations/{invitation_id}", response_model=OkOut, summary="撤回邀请（队长）"
)
def revoke_invitation(
    company_id: str,
    invitation_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    inv = db.get(Invitation, invitation_id)
    if inv is None or inv.company_id != company.id:
        raise NotFound("邀请不存在")
    inv.status = "revoked"
    return OkOut()


@router.post(
    "/{company_id}/invitations/{invitation_id}/deliveries",
    response_model=InvitationCreatedOut,
    summary="重发邀请／激活链接",
)
def redeliver_invitation(
    company_id: str,
    invitation_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> InvitationCreatedOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    inv = db.get(Invitation, invitation_id)
    if inv is None or inv.company_id != company.id:
        raise NotFound("邀请不存在")
    inv.status = "pending"
    token = _deliver(db, company, inv)
    return InvitationCreatedOut(
        invitation=InvitationOut.model_validate(inv),
        token=token if settings.environment != "prod" else "",
    )


# --- 邀请链接 ---------------------------------------------------------------


@router.post(
    "/{company_id}/invite-links", response_model=InviteLinkOut, summary="生成邀请链接（队长）"
)
def create_invite_link(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> InviteLinkOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    if company.roster_locked:
        raise Conflict("名单已锁定")
    token = new_token()
    db.add(
        InviteLink(
            company_id=company.id,
            token_hash=hash_token(token),
            created_by=principal.user.id,
        )
    )
    return InviteLinkOut(token=token, expires_at=None)


@router.delete("/{company_id}/invite-links", response_model=OkOut, summary="停用邀请链接（队长）")
def disable_invite_links(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    svc.require_captain(db, company.id, principal.user.id)
    now = datetime.now(UTC)
    for link in db.scalars(
        select(InviteLink).where(
            InviteLink.company_id == company.id, InviteLink.disabled_at.is_(None)
        )
    ).all():
        link.disabled_at = now
    return OkOut()


# --- 入队申请 ---------------------------------------------------------------


@router.get(
    "/{company_id}/join-requests",
    response_model=list[JoinRequestOut],
    summary="待处理的入队申请",
)
def list_join_requests(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> list[JoinRequestOut]:
    company = svc.company_by_ref(db, company_id)
    svc.require_member(db, company.id, principal.user.id)
    rows = db.scalars(
        select(JoinRequest)
        .where(JoinRequest.company_id == company.id, JoinRequest.status == "pending")
        .order_by(JoinRequest.created_at)
    ).all()
    return [JoinRequestOut.model_validate(row) for row in rows]


@router.post(
    "/{company_id}/join-requests",
    response_model=JoinRequestOut,
    status_code=201,
    summary="申请加入某公司",
)
def create_join_request(
    company_id: str,
    payload: JoinRequestIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> JoinRequestOut:
    company = svc.company_by_ref(db, company_id)
    event = db.get(Event, company.event_id)
    if event is None or not event.registration_open:
        raise Forbidden("报名已关闭")
    if company.roster_locked:
        raise Conflict("该公司名单已锁定")
    if svc.my_membership(db, principal.user.id) is not None:
        raise Conflict("你已经在公司中")
    if svc.seats_left(db, company, event) <= 0:
        raise Conflict("公司人数已满")
    if svc.pending_join_request(db, company.id, principal.user.id) is not None:
        raise Conflict("你已经申请过这家公司")
    row = JoinRequest(
        company_id=company.id, user_id=principal.user.id, message=payload.message
    )
    db.add(row)
    db.flush()
    return JoinRequestOut.model_validate(row)


def _decide_join_request(
    db: DbSession,
    company: Company,
    request_id: str,
    principal: Principal,
    *,
    accept: bool,
) -> OkOut:
    svc.require_captain(db, company.id, principal.user.id)
    row = db.get(JoinRequest, request_id)
    if row is None or row.company_id != company.id or row.status != "pending":
        raise NotFound("申请不存在或已处理")
    if accept:
        if company.roster_locked:
            raise Conflict("名单已锁定")
        user = db.get(User, row.user_id)
        if user is None:
            raise NotFound("申请人不存在")
        add_member(db, company, user)
        row.status = "accepted"
    else:
        row.status = "declined"
    return OkOut()


@router.post(
    "/{company_id}/join-requests/{request_id}/acceptance",
    response_model=OkOut,
    summary="同意入队申请（队长）",
)
def accept_join_request(
    company_id: str,
    request_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    return _decide_join_request(db, company, request_id, principal, accept=True)


@router.post(
    "/{company_id}/join-requests/{request_id}/decline",
    response_model=OkOut,
    summary="拒绝入队申请（队长）",
)
def decline_join_request(
    company_id: str,
    request_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = svc.company_by_ref(db, company_id)
    return _decide_join_request(db, company, request_id, principal, accept=False)


__all__ = ["router"]
