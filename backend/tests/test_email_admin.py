"""邮件中心：广播、受众、日志与模板。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.email import EmailMessage
from app.models.identity import User
from tests.conftest import login


def _staff(db: Session) -> User:
    row = db.scalar(select(User).where(User.email == "staff@example.com"))
    if row is None:
        row = User(
            email="staff@example.com",
            password_hash=hash_password("staff-password-123"),
            display_name="Staff",
            role="staff",
        )
        db.add(row)
        db.commit()
    return row


def test_estimate_and_send_broadcast(
    client: TestClient, db_session, event, user, mailer
) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    estimate = client.post(
        "/api/v1/admin/email/audience-estimates", json={"audience": "all"}
    ).json()
    assert estimate["recipients"] >= 2

    created = client.post(
        "/api/v1/admin/email/broadcasts",
        json={"title": "今晚开播", "body": "你好 {name}", "audience": "all"},
    ).json()
    assert created["status"] == "draft"

    sent = client.post(
        f"/api/v1/admin/email/broadcasts/{created['id']}/schedules", json={}
    )
    assert sent.status_code == 200, sent.text
    assert sent.json()["status"] == "sent"
    assert len(mailer.notices) >= 2

    messages = client.get("/api/v1/admin/email/messages").json()
    assert messages["counts"]["sent"] >= 2
    assert any("{name}" not in item["subject"] for item in messages["items"])


def test_test_send_and_preview(client: TestClient, db_session, event, mailer) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    created = client.post(
        "/api/v1/admin/email/broadcasts", json={"title": "测试", "body": "嗨"}
    ).json()

    assert client.post(f"/api/v1/admin/email/broadcasts/{created['id']}/tests").status_code == 200
    preview = client.post(
        "/api/v1/admin/email/broadcasts/previews",
        json={"title": "预览", "body": "你好 {name}"},
    ).json()
    assert preview["body"] == "你好 示例用户"


def test_templates_and_setup(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    templates = client.get("/api/v1/admin/email/templates").json()
    assert {row["key"] for row in templates} >= {"welcome", "results"}

    rendered = client.post(
        "/api/v1/admin/email/template-previews",
        json={"template": "welcome", "locale": "en", "props": {"name": "Will"}},
    ).json()
    assert "Will" in rendered["body"]

    setup = client.get("/api/v1/admin/email/setup").json()
    assert setup["driver"] in {"console", "remote"}


def test_resend_failed_message(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    row = EmailMessage(
        to_email="lost@example.com", subject="失败的信", body="正文", status="failed", error="boom"
    )
    db_session.add(row)
    db_session.commit()

    login(client, "staff@example.com", "staff-password-123")
    res = client.post(f"/api/v1/admin/email/messages/{row.id}/resends")
    assert res.status_code == 200
    db_session.refresh(row)
    assert row.status == "sent"


def test_email_admin_requires_staff(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    assert client.get("/api/v1/admin/email/broadcasts").status_code == 403
