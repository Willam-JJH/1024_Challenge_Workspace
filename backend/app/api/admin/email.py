"""管理台：邮件中心（广播、日志、模板、配额）。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends, Query
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_staff
from app.core.errors import Conflict, NotFound, Unprocessable
from app.models.email import Broadcast, EmailMessage
from app.models.event import Membership
from app.models.identity import User
from app.schemas.admin_email import (
    BroadcastIn,
    BroadcastOut,
    BroadcastPatchIn,
    EstimateIn,
    EstimateOut,
    MessageOut,
    MessagesOut,
    PreviewIn,
    PreviewOut,
    ScheduleIn,
    SetupOut,
    TemplateOut,
    TemplatePreviewIn,
)
from app.schemas.common import OkOut
from app.services import audit
from app.services import companies as company_service
from app.services.mailer import ConsoleMailer, get_mailer

router = APIRouter(prefix="/admin/email", tags=["admin"])

TEMPLATES = [
    ("welcome", "欢迎与激活", "账号创建后的欢迎邮件，含激活链接"),
    ("vote-reminder", "投票提醒", "投票截止前的提醒"),
    ("offline-reminder", "线下提醒", "场次开始前一天的提醒"),
    ("results", "结果公布", "入围与奖项公布通知"),
]

def _driver() -> str:
    return "console" if isinstance(get_mailer(), ConsoleMailer) else "remote"


def _recipients(db: DbSession, audience: str, locale: str) -> list[User]:
    stmt = select(User).where(User.status == "active")
    if audience == "participants":
        stmt = stmt.where(User.role.in_(["participant", "member", "captain"]))
    elif audience == "members":
        stmt = stmt.where(User.id.in_(select(Membership.user_id)))
    elif audience == "staff":
        stmt = stmt.where(User.role.in_(["staff", "admin"]))
    if locale != "all":
        stmt = stmt.where(User.locale == locale)
    return list(db.scalars(stmt).all())


def _render(text: str, user: User) -> str:
    return text.replace("{name}", user.display_name).replace("{email}", user.email)


def _send(db: DbSession, *, users: list[User], subject: str, body: str, broadcast_id: str | None):
    mailer = get_mailer()
    sent = failed = 0
    for user in users:
        rendered = _render(body, user)
        message = EmailMessage(
            broadcast_id=broadcast_id,
            to_email=user.email,
            subject=subject,
            body=rendered,
            status="queued",
        )
        db.add(message)
        try:
            mailer.send_notice(to=user.email, subject=subject, body=rendered)
        except Exception as exc:  # noqa: BLE001
            message.status = "failed"
            message.error = str(exc)[:500]
            failed += 1
        else:
            message.status = "sent"
            message.sent_at = datetime.now(UTC)
            sent += 1
    db.flush()
    return sent, failed


# --- 广播 -------------------------------------------------------------------


@router.get("/broadcasts", response_model=list[BroadcastOut], summary="广播列表")
def list_broadcasts(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> list[BroadcastOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Broadcast)
        .where(Broadcast.event_id == event.id)
        .order_by(Broadcast.created_at.desc())
    ).all()
    return [BroadcastOut.model_validate(row) for row in rows]


def _broadcast(db: DbSession, broadcast_id: str) -> Broadcast:
    row = db.get(Broadcast, broadcast_id)
    if row is None:
        raise NotFound("广播不存在")
    return row


@router.post("/broadcasts", response_model=BroadcastOut, status_code=201, summary="保存广播草稿")
def create_broadcast(
    payload: BroadcastIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> BroadcastOut:
    event = company_service.current_event(db)
    row = Broadcast(
        event_id=event.id,
        title=payload.title,
        body=payload.body,
        audience=payload.audience,
        locale=payload.locale,
        status="draft",
        created_by=principal.user.id,
    )
    db.add(row)
    db.flush()
    return BroadcastOut.model_validate(row)


@router.patch(
    "/broadcasts/{broadcast_id}", response_model=BroadcastOut, summary="编辑草稿或已排期广播"
)
def patch_broadcast(
    broadcast_id: str,
    payload: BroadcastPatchIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> BroadcastOut:
    row = _broadcast(db, broadcast_id)
    if row.status == "sent":
        raise Conflict("已发送的广播不能修改")
    for field in ("title", "body", "audience", "locale"):
        value = getattr(payload, field)
        if value is not None:
            setattr(row, field, value)
    db.flush()
    return BroadcastOut.model_validate(row)


@router.delete(
    "/broadcasts/{broadcast_id}", response_model=OkOut, summary="删除草稿或已取消的广播"
)
def delete_broadcast(
    broadcast_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _broadcast(db, broadcast_id)
    if row.status not in {"draft", "cancelled"}:
        raise Conflict("只能删除草稿或已取消的广播")
    db.delete(row)
    return OkOut()


@router.post(
    "/broadcasts/{broadcast_id}/cancellations", response_model=OkOut, summary="取消广播"
)
def cancel_broadcast(
    broadcast_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _broadcast(db, broadcast_id)
    if row.status == "sent":
        raise Conflict("已经发出去了")
    row.status = "cancelled"
    return OkOut()


@router.post(
    "/broadcasts/{broadcast_id}/schedules",
    response_model=BroadcastOut,
    summary="立即发送或定时发送",
)
def schedule_broadcast(
    broadcast_id: str,
    payload: ScheduleIn,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> BroadcastOut:
    row = _broadcast(db, broadcast_id)
    if row.status == "sent":
        raise Conflict("已经发送过了")

    if payload.scheduled_at is not None and payload.scheduled_at > datetime.now(UTC):
        row.status = "scheduled"
        row.scheduled_at = payload.scheduled_at
        db.flush()
        return BroadcastOut.model_validate(row)

    users = _recipients(db, row.audience, row.locale)
    if not users:
        raise Unprocessable("这个受众没有人")
    _send(db, users=users, subject=row.title, body=row.body, broadcast_id=row.id)
    row.status = "sent"
    row.sent_at = datetime.now(UTC)
    audit.record(
        db,
        actor_id=principal.user.id,
        action="email.broadcast",
        target_type="broadcast",
        target_id=row.id,
        note=f"{len(users)} 人",
    )
    db.flush()
    return BroadcastOut.model_validate(row)


@router.post(
    "/broadcasts/{broadcast_id}/tests", response_model=OkOut, summary="发一封给自己的测试"
)
def test_broadcast(
    broadcast_id: str,
    principal: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _broadcast(db, broadcast_id)
    _send(
        db,
        users=[principal.user],
        subject=f"[测试] {row.title}",
        body=row.body,
        broadcast_id=row.id,
    )
    return OkOut()


@router.post(
    "/broadcasts/previews", response_model=PreviewOut, summary="按收件人视角渲染广播"
)
def preview_broadcast(
    payload: PreviewIn, _: Principal = Depends(require_staff)
) -> PreviewOut:
    body = payload.body.replace("{name}", "示例用户")
    return PreviewOut(subject=payload.title, body=body)


@router.post("/audience-estimates", response_model=EstimateOut, summary="受众规模估算")
def estimate_audience(
    payload: EstimateIn,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> EstimateOut:
    users = _recipients(db, payload.audience, payload.locale)
    by_locale: dict[str, int] = {}
    for user in users:
        by_locale[user.locale] = by_locale.get(user.locale, 0) + 1
    return EstimateOut(recipients=len(users), by_locale=by_locale)


# --- 日志与模板 -------------------------------------------------------------


@router.get("/messages", response_model=MessagesOut, summary="邮件日志（最新在前）")
def list_messages(
    limit: int = Query(default=100, ge=1, le=500),
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> MessagesOut:
    rows = db.scalars(
        select(EmailMessage).order_by(EmailMessage.created_at.desc()).limit(limit)
    ).all()
    counts = {
        status: int(
            db.scalar(
                select(func.count()).select_from(EmailMessage).where(EmailMessage.status == status)
            )
            or 0
        )
        for status in ("queued", "sent", "failed")
    }
    return MessagesOut(
        items=[MessageOut.model_validate(row) for row in rows], counts=counts
    )


@router.post(
    "/messages/{message_id}/resends", response_model=OkOut, summary="重新发送失败的邮件"
)
def resend_message(
    message_id: str,
    _: Principal = Depends(require_staff),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(EmailMessage, message_id)
    if row is None:
        raise NotFound("邮件不存在")
    if row.status != "failed":
        raise Conflict("只有失败的邮件可以重发")
    get_mailer().send_notice(to=row.to_email, subject=row.subject, body=row.body)
    row.status = "sent"
    row.sent_at = datetime.now(UTC)
    row.error = None
    return OkOut()


@router.get("/templates", response_model=list[TemplateOut], summary="全部邮件模板")
def list_templates(_: Principal = Depends(require_staff)) -> list[TemplateOut]:
    return [TemplateOut(key=key, name=name, description=desc) for key, name, desc in TEMPLATES]


@router.post("/template-previews", response_model=PreviewOut, summary="渲染模板预览")
def preview_template(
    payload: TemplatePreviewIn, _: Principal = Depends(require_staff)
) -> PreviewOut:
    known = {key: name for key, name, _ in TEMPLATES}
    if payload.template not in known:
        raise NotFound("模板不存在")
    props = payload.props or {"name": "示例用户"}
    body = ", ".join(f"{key}: {value}" for key, value in props.items())
    return PreviewOut(
        subject=f"[{payload.locale}] {known[payload.template]}",
        body=f"{known[payload.template]}（{payload.locale}）\n{body}",
    )


@router.get("/setup", response_model=SetupOut, summary="邮件驱动与配额")
def setup(
    _: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> SetupOut:
    today = datetime.now(UTC).date()
    sent_today = int(
        db.scalar(
            select(func.count())
            .select_from(EmailMessage)
            .where(EmailMessage.sent_at >= datetime(today.year, today.month, today.day, tzinfo=UTC))
        )
        or 0
    )
    return SetupOut(driver=_driver(), daily_quota=None, sent_today=sent_today)


@router.get("/quota", summary="邮件服务商配额")
def quota(
    principal: Principal = Depends(require_staff), db: DbSession = Depends(get_db)
) -> dict:
    info = setup(principal, db)
    return {"driver": info.driver, "daily_quota": info.daily_quota, "sent_today": info.sent_today}
