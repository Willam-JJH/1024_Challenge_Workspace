"""管理台：账号。"""

import csv
import io
from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Query, Response
from sqlalchemy import func, or_, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_admin, require_staff
from app.core.errors import Conflict, Forbidden, NotFound
from app.models.event import Company, Membership
from app.models.identity import Session, User
from app.schemas.admin import (
    BulkIdsIn,
    BulkLocaleIn,
    BulkRoleIn,
    CreateUserIn,
    LocaleIn,
    MembershipBrief,
    ModeIn,
    RoleIn,
    UserDetail,
    UserRow,
)
from app.schemas.common import OkOut
from app.services import audit, codes

router = APIRouter(prefix="/admin/users", tags=["admin"])


def _user(db: DbSession, user_id: str) -> User:
    row = db.get(User, user_id)
    if row is None:
        raise NotFound("账号不存在")
    return row


def _revoke_sessions(db: DbSession, user_id: str) -> int:
    rows = db.scalars(
        select(Session).where(Session.user_id == user_id, Session.revoked_at.is_(None))
    ).all()
    now = datetime.now(UTC)
    for row in rows:
        row.revoked_at = now
    return len(rows)


@router.get("", response_model=list[UserRow], summary="搜索账号")
def search_users(
    q: str | None = Query(default=None, max_length=120),
    limit: int = Query(default=50, ge=1, le=200),
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[UserRow]:
    stmt = select(User).order_by(User.created_at.desc()).limit(limit)
    if q:
        pattern = f"%{q.lower()}%"
        stmt = stmt.where(
            or_(
                func.lower(User.email).like(pattern),
                func.lower(User.display_name).like(pattern),
            )
        )
    return [UserRow.model_validate(row) for row in db.scalars(stmt).all()]


@router.post("", response_model=UserRow, status_code=201, summary="创建账号（待激活）")
def create_user(
    payload: CreateUserIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> UserRow:
    email = payload.email.lower()
    if db.scalar(select(User).where(User.email == email)) is not None:
        raise Conflict("该邮箱已存在")
    user = User(
        email=email,
        password_hash="!pending",
        display_name=payload.display_name,
        locale=payload.locale,
        status="pending",
    )
    db.add(user)
    db.flush()
    codes.issue_code(db, email=email, purpose="register", locale=payload.locale)
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.create",
        target_type="user",
        target_id=user.id,
    )
    return UserRow.model_validate(user)


@router.get("/export.csv", summary="按筛选导出 CSV")
def export_csv(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> Response:
    buffer = io.StringIO()
    writer = csv.writer(buffer)
    writer.writerow(["email", "display_name", "role", "status", "mode", "locale", "created_at"])
    for user in db.scalars(select(User).order_by(User.created_at)).all():
        writer.writerow(
            [
                user.email,
                user.display_name,
                user.role,
                user.status,
                user.mode,
                user.locale,
                user.created_at.isoformat(),
            ]
        )
    return Response(
        buffer.getvalue(),
        media_type="text/csv; charset=utf-8",
        headers={"Content-Disposition": 'attachment; filename="users.csv"'},
    )


@router.get("/{user_id}", response_model=UserDetail, summary="账号档案")
def get_user(
    user_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> UserDetail:
    user = _user(db, user_id)
    rows = db.execute(
        select(Membership, Company)
        .join(Company, Company.id == Membership.company_id)
        .where(Membership.user_id == user.id)
    ).all()
    return UserDetail(
        **UserRow.model_validate(user).model_dump(),
        memberships=[
            MembershipBrief(company_id=company.id, company_name=company.name, role=row.role)
            for row, company in rows
        ],
    )


@router.get("/{user_id}/teams", response_model=list[MembershipBrief], summary="该账号所在的团队")
def user_teams(
    user_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[MembershipBrief]:
    _user(db, user_id)
    rows = db.execute(
        select(Membership, Company)
        .join(Company, Company.id == Membership.company_id)
        .where(Membership.user_id == user_id)
    ).all()
    return [
        MembershipBrief(company_id=company.id, company_name=company.name, role=row.role)
        for row, company in rows
    ]


@router.put("/{user_id}/role", response_model=OkOut, summary="修改全局角色")
def set_role(
    user_id: str,
    payload: RoleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if user_id == principal.user.id:
        raise Forbidden("不能修改自己的角色")
    user = _user(db, user_id)
    if payload.role == "admin" and principal.user.role != "admin":
        raise Forbidden("只有管理员可以授予管理员角色")
    user.role = payload.role
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.role",
        target_type="user",
        target_id=user.id,
        note=payload.role,
    )
    return OkOut()


@router.put("/{user_id}/mode", response_model=OkOut, summary="修改参与方式")
def set_mode(
    user_id: str,
    payload: ModeIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = _user(db, user_id)
    user.mode = payload.mode
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.mode",
        target_type="user",
        target_id=user.id,
        note=payload.mode,
    )
    return OkOut()


@router.put("/{user_id}/locale", response_model=OkOut, summary="设置邮件语言")
def set_locale(
    user_id: str,
    payload: LocaleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = _user(db, user_id)
    user.locale = payload.locale
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.locale",
        target_type="user",
        target_id=user.id,
        note=payload.locale,
    )
    return OkOut()


@router.post("/{user_id}/suspensions", response_model=OkOut, summary="停用账号")
def suspend_user(
    user_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if user_id == principal.user.id:
        raise Forbidden("不能停用自己")
    user = _user(db, user_id)
    user.status = "suspended"
    _revoke_sessions(db, user.id)
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.suspend",
        target_type="user",
        target_id=user.id,
    )
    return OkOut()


@router.delete("/{user_id}/suspensions", response_model=OkOut, summary="恢复账号")
def unsuspend_user(
    user_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = _user(db, user_id)
    user.status = "active"
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.unsuspend",
        target_type="user",
        target_id=user.id,
    )
    return OkOut()


@router.post("/{user_id}/sign-outs", response_model=OkOut, summary="强制退出全部会话")
def force_sign_out(
    user_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    _user(db, user_id)
    _revoke_sessions(db, user_id)
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.sign_out",
        target_type="user",
        target_id=user_id,
    )
    return OkOut()


@router.post(
    "/{user_id}/activation-deliveries", response_model=OkOut, summary="重发待激活邀请"
)
def resend_activation(
    user_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = _user(db, user_id)
    if user.status != "pending":
        raise Conflict("该账号不需要激活")
    codes.issue_code(db, email=user.email, purpose="register", locale=user.locale)
    return OkOut()


@router.delete("/{user_id}", response_model=OkOut, summary="移除账号")
def delete_user(
    user_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if user_id == principal.user.id:
        raise Forbidden("不能删除自己")
    user = _user(db, user_id)
    if user.role == "admin" and principal.user.role != "admin":
        raise Forbidden("只有管理员可以删除管理员")
    audit.record(
        db,
        actor_id=principal.user.id,
        action="user.delete",
        target_type="user",
        target_id=user.id,
        note=user.email,
    )
    db.delete(user)
    return OkOut()


@router.put("/roles", response_model=OkOut, summary="批量修改角色")
def bulk_role(
    payload: BulkRoleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if payload.role == "admin" and principal.user.role != "admin":
        raise Forbidden("只有管理员可以授予管理员角色")
    for user in db.scalars(select(User).where(User.id.in_(payload.ids))).all():
        user.role = payload.role
        audit.record(
            db,
            actor_id=principal.user.id,
            action="user.role",
            target_type="user",
            target_id=user.id,
            note=payload.role,
        )
    return OkOut()


@router.put("/locales", response_model=OkOut, summary="批量设置邮件语言")
def bulk_locale(
    payload: BulkLocaleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    for user in db.scalars(select(User).where(User.id.in_(payload.ids))).all():
        user.locale = payload.locale
    return OkOut()


@router.delete("", response_model=OkOut, summary="批量移除账号")
def bulk_delete(
    payload: BulkIdsIn,
    principal: Principal = Depends(require_admin),
    db: DbSession = Depends(get_db),
) -> OkOut:
    ids = [user_id for user_id in payload.ids if user_id != principal.user.id]
    for user in db.scalars(select(User).where(User.id.in_(ids))).all():
        audit.record(
            db,
            actor_id=principal.user.id,
            action="user.delete",
            target_type="user",
            target_id=user.id,
            note=user.email,
        )
        db.delete(user)
    return OkOut()
