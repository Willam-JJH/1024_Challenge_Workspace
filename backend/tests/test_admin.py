"""管理台：账号、开关、内容与审计。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.identity import User
from tests.conftest import login


def _staff(db: Session, role: str = "staff") -> User:
    email = f"{role}@example.com"
    row = db.scalar(select(User).where(User.email == email))
    if row is None:
        row = User(
            email=email,
            password_hash=hash_password(f"{role}-password-123"),
            display_name=role.title(),
            role=role,
        )
        db.add(row)
        db.commit()
    return row


def test_non_staff_cannot_use_admin(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    assert client.get("/api/v1/admin/users").status_code == 403
    assert client.get("/api/v1/admin/dashboard").status_code == 403


def test_user_administration(client: TestClient, db_session, event, user, other_user) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    users = client.get("/api/v1/admin/users", params={"q": "ana"}).json()
    assert [row["email"] for row in users] == ["ana@example.com"]

    detail = client.get(f"/api/v1/admin/users/{other_user.id}").json()
    assert detail["status"] == "active"

    res = client.put(f"/api/v1/admin/users/{other_user.id}/role", json={"role": "captain"})
    assert res.status_code == 200
    # 组织者不能授予管理员
    res = client.put(f"/api/v1/admin/users/{other_user.id}/role", json={"role": "admin"})
    assert res.status_code == 403

    res = client.post(f"/api/v1/admin/users/{other_user.id}/suspensions")
    assert res.status_code == 200
    assert client.post(
        "/api/v1/auth/sessions",
        json={"email": "ana@example.com", "password": "ana-password-123"},
    ).status_code == 401

    res = client.delete(f"/api/v1/admin/users/{other_user.id}/suspensions")
    assert res.status_code == 200

    res = client.put(
        f"/api/v1/admin/users/{other_user.id}/locale", json={"locale": "en"}
    )
    assert res.status_code == 200


def test_create_pending_user_sends_code(
    client: TestClient, db_session, event, mailer
) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.post(
        "/api/v1/admin/users",
        json={"email": "newbie@example.com", "display_name": "Newbie"},
    )
    assert res.status_code == 201, res.text
    assert res.json()["status"] == "pending"
    assert mailer.last_code("register")

    res = client.post(
        "/api/v1/admin/users", json={"email": "newbie@example.com", "display_name": "叠"}
    )
    assert res.status_code == 409


def test_event_switches_and_dashboard(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    res = client.put("/api/v1/admin/event/switches", json={"voting_open": False})
    assert res.status_code == 200
    assert client.get("/api/v1/events/current").json()["voting_open"] is False

    dashboard = client.get("/api/v1/admin/dashboard").json()
    assert dashboard["users"] >= 1

    audit_rows = client.get("/api/v1/admin/audit").json()
    assert any(row["action"] == "event.switches" for row in audit_rows)


def test_feature_flags(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    res = client.post(
        "/api/v1/admin/feature-flags", json={"key": "exhibition-open", "enabled": False}
    )
    assert res.status_code == 201, res.text
    flag_id = res.json()["id"]

    res = client.post("/api/v1/admin/feature-flags", json={"key": "exhibition-open"})
    assert res.status_code == 409

    res = client.patch(f"/api/v1/admin/feature-flags/{flag_id}", json={"enabled": True})
    assert res.json()["enabled"] is True


def test_announcements_publish_and_visibility(
    client: TestClient, db_session, event
) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    draft = client.post(
        "/api/v1/admin/announcements",
        json={"title": "草稿", "body": "还没发", "publish": False},
    ).json()
    published = client.post(
        "/api/v1/admin/announcements",
        json={"title": "发布了", "body": "现在可见", "publish": True},
    ).json()

    public = client.get("/api/v1/announcements").json()
    assert [row["title"] for row in public] == ["发布了"]

    res = client.patch(
        f"/api/v1/admin/announcements/{draft['id']}",
        json={"title": "草稿", "body": "补发", "publish": True},
    )
    assert res.status_code == 200
    assert len(client.get("/api/v1/announcements").json()) == 2
    assert published["status"] == "published"


def test_sponsor_order(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    first = client.post("/api/v1/admin/sponsors", json={"name": "A"}).json()
    second = client.post("/api/v1/admin/sponsors", json={"name": "B"}).json()
    assert [row["name"] for row in client.get("/api/v1/sponsors").json()] == ["A", "B"]

    client.put("/api/v1/admin/sponsors/order", json={"ids": [second["id"], first["id"]]})
    assert [row["name"] for row in client.get("/api/v1/sponsors").json()] == ["B", "A"]
