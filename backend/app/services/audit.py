"""审计日志写入。"""

from sqlalchemy.orm import Session as DbSession

from app.models.audit import AuditLog


def record(
    db: DbSession,
    *,
    actor_id: str | None,
    action: str,
    target_type: str,
    target_id: str | None = None,
    note: str | None = None,
) -> None:
    db.add(
        AuditLog(
            actor_id=actor_id,
            action=action,
            target_type=target_type,
            target_id=target_id,
            note=note,
        )
    )
