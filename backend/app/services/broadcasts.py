"""广播：收件人筛选、投递、定时发送。

投递语义与 Go 实现（``backend-go/internal/api/broadcast_worker.go``）一致：
先原子认领（``scheduled`` → ``sending``），再逐个收件人写日志并发信，
最后置 ``sent``。认领让同时在跑的 Python / Go 实例不会重复发送。
"""

import logging
from datetime import UTC, datetime, timedelta

from sqlalchemy import select, update
from sqlalchemy.orm import Session as DbSession

from app.models.email import Broadcast, EmailMessage
from app.models.event import Membership
from app.models.identity import User
from app.services.mailer import get_mailer

log = logging.getLogger("challenge-api.broadcast")

MAX_PER_TICK = 5
# 卡在 sending 超过这个时长就当作已处理，避免一直卡住；部分发送的情况看邮件日志
STUCK_AFTER = timedelta(hours=1)
ERROR_LIMIT = 500


def recipients(db: DbSession, audience: str, locale: str) -> list[User]:
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


def render(text: str, user: User) -> str:
    return text.replace("{name}", user.display_name).replace("{email}", user.email)


def send(
    db: DbSession, *, users: list[User], subject: str, body: str, broadcast_id: str | None
) -> tuple[int, int]:
    """逐个投递；无论成败都留一条 email_messages。返回 (sent, failed)。"""
    mailer = get_mailer()
    sent = failed = 0
    for user in users:
        rendered = render(body, user)
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
            message.error = str(exc)[:ERROR_LIMIT]
            failed += 1
        else:
            message.status = "sent"
            message.sent_at = datetime.now(UTC)
            sent += 1
    db.flush()
    return sent, failed


def claim(db: DbSession, broadcast_id: str, now: datetime) -> bool:
    """把一个到点的 scheduled 广播认领成 sending；别人先抢到就返回 False。"""
    # 用带条件的 UPDATE 做比较交换，两个进程同时抢也只有一个成功
    updated = db.execute(
        update(Broadcast)
        .where(Broadcast.id == broadcast_id, Broadcast.status == "scheduled")
        .values(status="sending", updated_at=now)
    )
    db.flush()
    return updated.rowcount == 1


def deliver(db: DbSession, broadcast: Broadcast, now: datetime) -> tuple[int, int]:
    users = recipients(db, broadcast.audience, broadcast.locale)
    if not users:
        log.warning("broadcast %s 到点发送时没有收件人", broadcast.id)
        sent = failed = 0
    else:
        sent, failed = send(
            db,
            users=users,
            subject=broadcast.title,
            body=broadcast.body,
            broadcast_id=broadcast.id,
        )
    broadcast.status = "sent"
    broadcast.sent_at = now
    db.flush()
    return sent, failed


def recover_stuck(db: DbSession, now: datetime) -> int:
    """把长时间停在 sending 的广播收尾，避免状态永远卡住。"""
    cutoff = now - STUCK_AFTER
    stuck = db.scalars(
        select(Broadcast).where(Broadcast.status == "sending", Broadcast.updated_at < cutoff)
    ).all()
    for broadcast in stuck:
        log.warning("broadcast %s 停在 sending 超过 1 小时，按已发送收尾", broadcast.id)
        broadcast.status = "sent"
        broadcast.sent_at = now
    db.flush()
    return len(stuck)


def run_due(db: DbSession, now: datetime | None = None) -> int:
    """处理到点的广播，返回本轮实际发出的条数。"""
    now = now or datetime.now(UTC)
    recover_stuck(db, now)
    due = db.scalars(
        select(Broadcast)
        .where(
            Broadcast.status == "scheduled",
            Broadcast.scheduled_at.is_not(None),
            Broadcast.scheduled_at <= now,
        )
        .order_by(Broadcast.scheduled_at)
        .limit(MAX_PER_TICK)
    ).all()
    handled = 0
    for broadcast in due:
        if not claim(db, broadcast.id, now):
            continue
        try:
            sent, failed = deliver(db, broadcast, now)
            db.commit()
        except Exception:  # noqa: BLE001
            # 回滚会把认领一起撤掉，下一轮重试（与 Go 侧显式退回等价）
            db.rollback()
            log.exception("广播 %s 发送失败，已退回 scheduled", broadcast.id)
            continue
        handled += 1
        log.info("broadcast %s 已发送 sent=%d failed=%d", broadcast.id, sent, failed)
    return handled
