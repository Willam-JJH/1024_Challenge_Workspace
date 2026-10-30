"""管理台：公司档案、名单与作品。"""

from fastapi import APIRouter, Depends
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound
from app.models.event import Company, Membership
from app.models.identity import User
from app.models.media import MediaAsset
from app.models.project import Project, ProjectVersion
from app.schemas.common import OkOut, ProjectOut
from app.schemas.company import MemberOut, MoveIn, RosterLockIn, TransferIn
from app.schemas.project import VersionOut
from app.services import audit
from app.services import companies as company_service

router = APIRouter(tags=["admin"])


def _company(db: DbSession, company_id: str) -> Company:
    company = db.get(Company, company_id)
    if company is None:
        raise NotFound("公司不存在")
    return company


def _project_of(db: DbSession, company_id: str) -> Project | None:
    return db.scalar(select(Project).where(Project.company_id == company_id))


@router.get("/admin/companies", summary="当前赛事的公司")
def list_companies(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[dict]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Company).where(Company.event_id == event.id).order_by(Company.created_at)
    ).all()
    out = []
    for company in rows:
        members = db.execute(
            select(Membership, User)
            .join(User, User.id == Membership.user_id)
            .where(Membership.company_id == company.id)
        ).all()
        project = _project_of(db, company.id)
        out.append(
            {
                "id": company.id,
                "name": company.name,
                "slug": company.slug,
                "captain_id": company.captain_id,
                "roster_locked": company.roster_locked,
                "members": [
                    {"user_id": user.id, "display_name": user.display_name, "role": member.role}
                    for member, user in members
                ],
                "project": (
                    {"id": project.id, "title": project.title, "status": project.status}
                    if project is not None
                    else None
                ),
            }
        )
    return out


@router.get("/admin/companies/{company_id}", summary="公司档案")
def get_company(
    company_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> dict:
    company = _company(db, company_id)
    members = db.execute(
        select(Membership, User)
        .join(User, User.id == Membership.user_id)
        .where(Membership.company_id == company.id)
    ).all()
    project = _project_of(db, company.id)
    return {
        "id": company.id,
        "name": company.name,
        "slug": company.slug,
        "blurb": company.blurb,
        "captain_id": company.captain_id,
        "roster_locked": company.roster_locked,
        "members": [
            MemberOut(user_id=user.id, display_name=user.display_name, role=member.role)
            for member, user in members
        ],
        "project": ProjectOut.model_validate(project) if project is not None else None,
    }


@router.post(
    "/admin/companies/{company_id}/dissolutions", response_model=OkOut, summary="强制解散公司"
)
def dissolve(
    company_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = _company(db, company_id)
    for membership in db.scalars(
        select(Membership).where(Membership.company_id == company.id)
    ).all():
        db.delete(membership)
    project = _project_of(db, company.id)
    if project is not None:
        project.status = "draft"
    company.roster_locked = True
    audit.record(
        db,
        actor_id=principal.user.id,
        action="company.dissolve",
        target_type="company",
        target_id=company.id,
        note=company.name,
    )
    return OkOut()


@router.put(
    "/admin/companies/{company_id}/roster-lock", response_model=OkOut, summary="锁定／解锁名单"
)
def roster_lock(
    company_id: str,
    payload: RosterLockIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = _company(db, company_id)
    company.roster_locked = payload.locked
    return OkOut()


@router.post(
    "/admin/companies/{company_id}/captain-transfers",
    response_model=OkOut,
    summary="指定队长",
)
def transfer_captain(
    company_id: str,
    payload: TransferIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = _company(db, company_id)
    membership = company_service.membership_of(db, company.id, payload.user_id)
    if membership is None:
        raise NotFound("对方不是公司成员")
    for row in db.scalars(
        select(Membership).where(
            Membership.company_id == company.id, Membership.role == "captain"
        )
    ).all():
        row.role = "member"
    membership.role = "captain"
    company.captain_id = membership.user_id
    audit.record(
        db,
        actor_id=principal.user.id,
        action="company.captain",
        target_type="company",
        target_id=company.id,
        note=membership.user_id,
    )
    return OkOut()


@router.post(
    "/admin/companies/{company_id}/submitter-appointments",
    response_model=OkOut,
    summary="指定提交人",
)
def appoint_submitter(
    company_id: str,
    payload: TransferIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    company = _company(db, company_id)
    membership = company_service.membership_of(db, company.id, payload.user_id)
    if membership is None:
        raise NotFound("对方不是公司成员")
    for row in db.scalars(
        select(Membership).where(
            Membership.company_id == company.id, Membership.role == "submitter"
        )
    ).all():
        row.role = "member"
    membership.role = "submitter"
    return OkOut()


@router.post("/admin/members/moves", response_model=OkOut, summary="移动或移出成员")
def move_member(
    payload: MoveIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    user = db.get(User, payload.user_id)
    if user is None:
        raise NotFound("账号不存在")
    current = company_service.membership_of(db, payload.from_company_id, user.id)
    if current is None:
        raise NotFound("该账号不在原公司")

    if payload.to_company_id:
        target = _company(db, payload.to_company_id)
        event = company_service.current_event(db)
        if company_service.seats_left(db, target, event) <= 0:
            raise Conflict("目标公司人数已满")
        current.company_id = target.id
        current.role = "member"
    else:
        db.delete(current)
        user.role = "participant"
    audit.record(
        db,
        actor_id=principal.user.id,
        action="member.move",
        target_type="user",
        target_id=user.id,
        note=f"{payload.from_company_id} -> {payload.to_company_id}",
    )
    return OkOut()


@router.get("/admin/projects", response_model=list[ProjectOut], summary="全部作品（含草稿）")
def list_projects(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[ProjectOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Project).where(Project.event_id == event.id).order_by(Project.updated_at.desc())
    ).all()
    return [ProjectOut.model_validate(row) for row in rows]


@router.get("/admin/projects/{project_id}", response_model=ProjectOut, summary="作品档案")
def get_project(
    project_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> ProjectOut:
    project = db.get(Project, project_id)
    if project is None:
        raise NotFound("作品不存在")
    return ProjectOut.model_validate(project)


@router.get(
    "/admin/projects/{project_id}/versions",
    response_model=list[VersionOut],
    summary="作品版本列表",
)
def list_versions(
    project_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> list[VersionOut]:
    rows = db.scalars(
        select(ProjectVersion)
        .where(ProjectVersion.project_id == project_id)
        .order_by(ProjectVersion.version.desc())
    ).all()
    return [VersionOut.model_validate(row) for row in rows]


@router.get(
    "/admin/projects/{project_id}/versions/{version}",
    response_model=VersionOut,
    summary="单个已发布版本",
)
def get_version(
    project_id: str,
    version: int,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> VersionOut:
    row = db.scalar(
        select(ProjectVersion).where(
            ProjectVersion.project_id == project_id, ProjectVersion.version == version
        )
    )
    if row is None:
        raise NotFound("版本不存在")
    return VersionOut.model_validate(row)


@router.delete("/admin/media/{asset_id}", response_model=OkOut, summary="下架文件")
def remove_asset(
    asset_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(MediaAsset, asset_id)
    if row is None:
        raise NotFound("素材不存在")
    row.status = "removed"
    audit.record(
        db,
        actor_id=principal.user.id,
        action="media.remove",
        target_type="media_asset",
        target_id=row.id,
        note=row.filename,
    )
    return OkOut()
