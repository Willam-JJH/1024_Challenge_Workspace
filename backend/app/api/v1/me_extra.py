"""个人中心：公司关系、参与方式、邀请、通知、护照、注销与介绍任务。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, NotFound, Unprocessable
from app.models.event import Company, Invitation, JoinRequest
from app.models.exhibition import Introduction, Visit
from app.models.identity import User
from app.models.notify import Notification
from app.models.project import Project
from app.schemas.common import OkOut
from app.schemas.exhibition import IntroductionOut
from app.schemas.me import (
    DeletionOut,
    DigestOut,
    DigestPatchIn,
    InvitationRow,
    MembershipOut,
    NotificationOut,
    ParticipationIn,
)
from app.services import companies as company_service
from app.services.companies import add_member

router = APIRouter(prefix="/me", tags=["me"])


@router.get("/membership", summary="我在当前赛事的公司关系")
def membership(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> dict:
    row = company_service.my_membership(db, principal.user.id)
    if row is None:
        return {"membership": None}
    company = db.get(Company, row.company_id)
    return {
        "membership": MembershipOut(
            company_id=company.id,
            company_name=company.name,
            company_slug=company.slug,
            role=row.role,
            captain=row.role == "captain",
        ).model_dump()
    }


@router.put("/participation", summary="切换旁观／参赛")
def participation(
    payload: ParticipationIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = principal.user
    if payload.mode == "participant":
        if user.role == "participant":
            user.role = "member"
    else:
        if company_service.my_membership(db, user.id) is not None:
            raise Conflict("还在公司里，先退出再切换")
        user.role = "participant"
    db.add(user)
    return OkOut()


@router.get("/invitations", response_model=list[InvitationRow], summary="发给我的邀请")
def my_invitations(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[InvitationRow]:
    rows = db.scalars(
        select(Invitation).where(
            Invitation.email == principal.user.email.lower(),
            Invitation.status == "pending",
        )
    ).all()
    out = []
    for row in rows:
        company = db.get(Company, row.company_id)
        out.append(
            InvitationRow(
                invitation_id=row.id,
                company_id=row.company_id,
                company_name=company.name if company else "",
                email=row.email,
                status=row.status,
            )
        )
    return out


@router.post("/invitations/{invitation_id}/acceptance", response_model=OkOut, summary="接受邀请")
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


@router.post("/invitations/{invitation_id}/decline", response_model=OkOut, summary="拒绝邀请")
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


@router.get("/join-requests", summary="我发出的入队申请")
def my_join_requests(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[dict]:
    rows = db.scalars(
        select(JoinRequest).where(
            JoinRequest.user_id == principal.user.id, JoinRequest.status == "pending"
        )
    ).all()
    out = []
    for row in rows:
        company = db.get(Company, row.company_id)
        out.append(
            {
                "id": row.id,
                "company_id": row.company_id,
                "company_name": company.name if company else "",
                "status": row.status,
            }
        )
    return out


@router.delete("/join-requests/{request_id}", response_model=OkOut, summary="撤回入队申请")
def withdraw_join_request(
    request_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(JoinRequest, request_id)
    if row is None or row.user_id != principal.user.id or row.status != "pending":
        raise NotFound("申请不存在")
    row.status = "withdrawn"
    return OkOut()


@router.get("/notifications", response_model=list[NotificationOut], summary="我的通知")
def notifications(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[NotificationOut]:
    rows = db.scalars(
        select(Notification)
        .where(Notification.user_id == principal.user.id)
        .order_by(Notification.created_at.desc())
        .limit(100)
    ).all()
    return [NotificationOut.model_validate(row) for row in rows]


@router.post("/notifications/read", response_model=OkOut, summary="把通知标记为已读")
def read_notifications(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> OkOut:
    now = datetime.now(UTC)
    for row in db.scalars(
        select(Notification).where(
            Notification.user_id == principal.user.id, Notification.read_at.is_(None)
        )
    ).all():
        row.read_at = now
    return OkOut()


@router.get("/digest-preferences", response_model=DigestOut, summary="邮件摘要偏好")
def get_digest(principal: Principal = Depends(require_auth)) -> DigestOut:
    return DigestOut(comments=principal.user.digest_comments)


@router.put("/digest-preferences", response_model=DigestOut, summary="开关每日评论摘要")
def put_digest(
    payload: DigestPatchIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> DigestOut:
    principal.user.digest_comments = payload.comments
    db.add(principal.user)
    return DigestOut(comments=payload.comments)


@router.get("/passport", summary="我的参观护照")
def passport(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> dict:
    event = company_service.current_event(db)
    rows = db.execute(
        select(Visit, Project, Company)
        .join(Project, Project.id == Visit.project_id)
        .join(Company, Company.id == Project.company_id)
        .where(Visit.visitor_id == principal.user.id, Visit.event_id == event.id)
        .order_by(Visit.created_at)
    ).all()
    items = [
        {
            "project_id": project.id,
            "company_name": company.name,
            "project_title": project.title,
            "note": visit.note,
            "created_at": visit.created_at.isoformat(),
        }
        for visit, project, company in rows
    ]
    return {
        "items": items,
        "needed": 3,
        "complete": len({item["company_name"] for item in items}) >= 3,
    }


@router.post("/deletion-request", response_model=DeletionOut, summary="申请注销账号")
def request_deletion(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> DeletionOut:
    principal.user.deletion_requested_at = datetime.now(UTC)
    db.add(principal.user)
    return DeletionOut(requested_at=principal.user.deletion_requested_at)


@router.delete("/deletion-request", response_model=DeletionOut, summary="撤销注销申请")
def cancel_deletion(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> DeletionOut:
    principal.user.deletion_requested_at = None
    db.add(principal.user)
    return DeletionOut(requested_at=None)


@router.get(
    "/exhibition-assignment",
    response_model=list[IntroductionOut],
    summary="与我公司相关的介绍任务",
)
def exhibition_assignment(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[IntroductionOut]:
    membership = company_service.my_membership(db, principal.user.id)
    if membership is None:
        return []
    rows = db.scalars(
        select(Introduction).where(
            (Introduction.introducer_company_id == membership.company_id)
            | (Introduction.target_company_id == membership.company_id)
        )
    ).all()
    out = []
    for row in rows:
        introducer = db.get(Company, row.introducer_company_id)
        target = db.get(Company, row.target_company_id)
        out.append(
            IntroductionOut(
                id=row.id,
                role="introducer"
                if row.introducer_company_id == membership.company_id
                else "target",
                introducer_company=introducer.name if introducer else "",
                target_company=target.name if target else "",
                text=row.text,
                reply=row.reply,
                status=row.status,
            )
        )
    return out


__all__ = ["User"]
