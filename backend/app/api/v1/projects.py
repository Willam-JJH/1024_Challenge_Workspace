"""作品：画廊、草稿、编辑锁、发布与版本。

并发模型：草稿写入必须持有编辑锁（``X-Edit-Token``），并带上 ``base_revision``；
修订号不一致返回 409，由客户端重新拉取草稿后再保存。
"""

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends, Header
from sqlalchemy import delete, func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, Forbidden, NotFound, Unprocessable
from app.core.security import hash_token, new_token
from app.models.event import Company, Membership
from app.models.project import Project, ProjectEditSession, ProjectVersion
from app.schemas.common import OkOut, ProjectOut
from app.schemas.project import (
    AttendanceIn,
    DraftSavedOut,
    EditSessionIn,
    EditSessionOut,
    ProjectCardOut,
    ProjectDraftIn,
    PublishOut,
    VersionOut,
)

router = APIRouter(prefix="/projects", tags=["projects"])

LOCK_TTL = timedelta(minutes=2)
EDITOR_ROLES = {"captain", "submitter"}

DRAFT_SCHEMA = {
    "type": "object",
    "required": ["title"],
    "properties": {
        "title": {"type": "string", "maxLength": 160},
        "summary": {"type": "string", "maxLength": 2000},
        "content": {"type": "string", "maxLength": 200000, "description": "Markdown"},
    },
}


def _membership(db: DbSession, user_id: str) -> Membership:
    row = db.scalar(select(Membership).where(Membership.user_id == user_id))
    if row is None:
        raise NotFound("你还没有公司，先组建或加入一家")
    return row


def _require_editor(db: DbSession, project: Project, principal: Principal) -> None:
    if principal.user.role in {"staff", "admin"}:
        return
    row = db.scalar(
        select(Membership).where(
            Membership.company_id == project.company_id, Membership.user_id == principal.user.id
        )
    )
    if row is None:
        raise Forbidden("不是该公司成员")
    if row.role not in EDITOR_ROLES:
        raise Forbidden("只有队长或提交人可以编辑作品")


def _require_reader(db: DbSession, project: Project, principal: Principal) -> None:
    if principal.user.role in {"staff", "admin"}:
        return
    row = db.scalar(
        select(Membership).where(
            Membership.company_id == project.company_id, Membership.user_id == principal.user.id
        )
    )
    if row is None:
        raise Forbidden("不是该公司成员")


def _project_or_404(db: DbSession, project_id: str) -> Project:
    project = db.get(Project, project_id)
    if project is None:
        raise NotFound("作品不存在")
    return project


def _ensure_lock(db: DbSession, project_id: str, token: str | None) -> ProjectEditSession:
    if not token:
        raise Conflict("缺少编辑锁，请先获取")
    session = db.scalar(
        select(ProjectEditSession).where(
            ProjectEditSession.project_id == project_id,
            ProjectEditSession.token_hash == hash_token(token),
        )
    )
    if session is None:
        raise Conflict("编辑锁无效")
    if session.expires_at < datetime.now(UTC):
        raise Conflict("编辑锁已过期，请重新获取")
    return session


@router.get("/mine", response_model=ProjectOut, summary="我公司的作品草稿（首次访问自动创建）")
def my_project(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> ProjectOut:
    membership = _membership(db, principal.user.id)
    project = db.scalar(select(Project).where(Project.company_id == membership.company_id))
    if project is None:
        company = db.get(Company, membership.company_id)
        project = Project(event_id=company.event_id, company_id=company.id)
        db.add(project)
        db.flush()
    return ProjectOut.model_validate(project)


@router.get("/draft-schema", summary="草稿字段的 JSON Schema（供代理使用）")
def draft_schema() -> dict:
    return DRAFT_SCHEMA


@router.get("", response_model=list[ProjectCardOut], summary="已发布作品画廊")
def gallery(db: DbSession = Depends(get_db)) -> list[ProjectCardOut]:
    rows = db.execute(
        select(Project, Company)
        .join(Company, Company.id == Project.company_id)
        .where(Project.status == "published")
        .order_by(Project.updated_at.desc())
        .limit(100)
    ).all()
    return [
        ProjectCardOut(
            **ProjectOut.model_validate(project).model_dump(),
            company_name=company.name,
            company_slug=company.slug,
        )
        for project, company in rows
    ]


@router.get("/{project_id}", response_model=ProjectOut, summary="草稿视图（成员与管理员）")
def get_project(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> ProjectOut:
    project = _project_or_404(db, project_id)
    _require_reader(db, project, principal)
    return ProjectOut.model_validate(project)


# --- 编辑锁 -----------------------------------------------------------------


@router.post(
    "/{project_id}/edit-sessions", response_model=EditSessionOut, summary="获取编辑锁"
)
def acquire_lock(
    project_id: str,
    payload: EditSessionIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> EditSessionOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)

    now = datetime.now(UTC)
    db.execute(
        delete(ProjectEditSession).where(
            ProjectEditSession.project_id == project.id, ProjectEditSession.expires_at < now
        )
    )
    active = db.scalar(
        select(ProjectEditSession).where(ProjectEditSession.project_id == project.id)
    )
    if active is not None and active.user_id != principal.user.id and not payload.take_over:
        raise Conflict("其他标签页正在编辑，请稍后再试或选择接管")

    if active is not None:
        db.delete(active)

    token = new_token()
    session = ProjectEditSession(
        project_id=project.id,
        user_id=principal.user.id,
        token_hash=hash_token(token),
        expires_at=now + LOCK_TTL,
    )
    db.add(session)
    db.flush()
    return EditSessionOut(id=session.id, token=token, expires_at=session.expires_at)


@router.post(
    "/{project_id}/edit-sessions/{session_id}/heartbeats",
    response_model=EditSessionOut,
    summary="续租编辑锁",
)
def heartbeat_lock(
    project_id: str,
    session_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
    x_edit_token: str | None = Header(default=None),
) -> EditSessionOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    session = _ensure_lock(db, project.id, x_edit_token)
    if session.id != session_id or session.user_id != principal.user.id:
        raise Conflict("编辑锁不属于这个会话")
    session.expires_at = datetime.now(UTC) + LOCK_TTL
    return EditSessionOut(
        id=session.id, token=x_edit_token or "", expires_at=session.expires_at
    )


@router.delete(
    "/{project_id}/edit-sessions/{session_id}", response_model=OkOut, summary="释放编辑锁"
)
def release_lock(
    project_id: str,
    session_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
    x_edit_token: str | None = Header(default=None),
) -> OkOut:
    project = _project_or_404(db, project_id)
    session = db.get(ProjectEditSession, session_id)
    if (
        session is not None
        and session.project_id == project.id
        and (session.user_id == principal.user.id or principal.user.role in {"staff", "admin"})
    ):
        db.delete(session)
    return OkOut()


# --- 草稿与发布 -------------------------------------------------------------


@router.patch("/{project_id}/draft", response_model=DraftSavedOut, summary="自动保存草稿")
def save_draft(
    project_id: str,
    payload: ProjectDraftIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
    x_edit_token: str | None = Header(default=None),
) -> DraftSavedOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    _ensure_lock(db, project.id, x_edit_token)

    if payload.base_revision != project.revision:
        raise Conflict("修订号已过期，请重新拉取草稿")

    project.title = payload.title
    project.summary = payload.summary
    project.content = payload.content
    project.revision += 1
    db.flush()
    return DraftSavedOut(revision=project.revision, updated_at=project.updated_at)


@router.post("/{project_id}/publications", response_model=PublishOut, summary="提交／更新作品")
def publish(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> PublishOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    if not project.title.strip():
        raise Unprocessable("标题不能为空")

    next_version = (
        int(
            db.scalar(
                select(func.coalesce(func.max(ProjectVersion.version), 0)).where(
                    ProjectVersion.project_id == project.id
                )
            )
            or 0
        )
        + 1
    )
    version = ProjectVersion(
        project_id=project.id,
        version=next_version,
        title=project.title,
        summary=project.summary,
        content=project.content,
        published_at=datetime.now(UTC),
        published_by=principal.user.id,
    )
    db.add(version)
    project.status = "published"
    project.live_version = next_version
    db.flush()
    return PublishOut(
        project=ProjectOut.model_validate(project), version=VersionOut.model_validate(version)
    )


@router.get(
    "/{project_id}/versions", response_model=list[VersionOut], summary="已发布版本（最新在前）"
)
def list_versions(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> list[VersionOut]:
    project = _project_or_404(db, project_id)
    _require_reader(db, project, principal)
    rows = db.scalars(
        select(ProjectVersion)
        .where(ProjectVersion.project_id == project.id)
        .order_by(ProjectVersion.version.desc())
    ).all()
    return [VersionOut.model_validate(row) for row in rows]


@router.get(
    "/{project_id}/versions/{version}", response_model=VersionOut, summary="单个已发布版本"
)
def get_version(
    project_id: str,
    version: int,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> VersionOut:
    project = _project_or_404(db, project_id)
    _require_reader(db, project, principal)
    row = db.scalar(
        select(ProjectVersion).where(
            ProjectVersion.project_id == project.id, ProjectVersion.version == version
        )
    )
    if row is None:
        raise NotFound("版本不存在")
    return VersionOut.model_validate(row)


@router.post(
    "/{project_id}/versions/{version}/restorations",
    response_model=DraftSavedOut,
    summary="把某个版本复制回草稿",
)
def restore_version(
    project_id: str,
    version: int,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> DraftSavedOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    row = db.scalar(
        select(ProjectVersion).where(
            ProjectVersion.project_id == project.id, ProjectVersion.version == version
        )
    )
    if row is None:
        raise NotFound("版本不存在")
    project.title = row.title
    project.summary = row.summary
    project.content = row.content
    project.revision += 1
    db.flush()
    return DraftSavedOut(revision=project.revision, updated_at=project.updated_at)


# --- 出席 -------------------------------------------------------------------


@router.get("/{project_id}/attendance", summary="Pitching Day 出席意向")
def get_attendance(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> dict:
    project = _project_or_404(db, project_id)
    _require_reader(db, project, principal)
    return {"attendance": project.attendance}


@router.put("/{project_id}/attendance", summary="回答出席问题（提交人或队长）")
def set_attendance(
    project_id: str,
    payload: AttendanceIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> dict:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    project.attendance = payload.attendance
    db.flush()
    return {"attendance": project.attendance}


@router.post(
    "/{project_id}/agent-draft-approvals",
    response_model=OkOut,
    summary="标记代理草稿已由人复核",
)
def approve_agent_draft(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    project = _project_or_404(db, project_id)
    _require_editor(db, project, principal)
    project.agent_reviewed = True
    db.flush()
    return OkOut()
