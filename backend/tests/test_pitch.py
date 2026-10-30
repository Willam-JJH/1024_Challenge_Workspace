"""直播页：流源、留言与开播开关。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
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


def test_public_state_defaults(client: TestClient, event) -> None:
    res = client.get("/api/v1/pitch")
    assert res.status_code == 200, res.text
    body = res.json()
    assert body["on_air"] is False
    assert body["sources"] == []
    assert body["messages"] == []


def test_staff_controls_sources_and_air(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    res = client.patch("/api/v1/admin/pitch", json={"on_air": True, "title": "开播了"})
    assert res.status_code == 200
    assert res.json()["on_air"] is True

    res = client.post(
        "/api/v1/admin/pitch/sources",
        json={"kind": "bilibili", "name": "主直播间", "url": "https://live.bilibili.com/1"},
    )
    assert res.status_code == 201, res.text
    first = res.json()

    res = client.post(
        "/api/v1/admin/pitch/sources",
        json={
            "kind": "youtube",
            "name": "Global",
            "url": "https://youtube.com/live/x",
            "region": "global",
        },
    )
    second = res.json()

    public = client.get("/api/v1/pitch").json()
    assert [s["name"] for s in public["sources"]] == ["主直播间", "Global"]
    assert public["title"] == "开播了"

    client.post(f"/api/v1/admin/pitch/sources/{second['id']}/moves", json={"direction": "up"})
    public = client.get("/api/v1/pitch").json()
    assert [s["name"] for s in public["sources"]] == ["Global", "主直播间"]
    assert first["id"] != second["id"]


def test_non_staff_cannot_manage(client: TestClient, db_session, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    res = client.patch("/api/v1/admin/pitch", json={"on_air": True})
    assert res.status_code == 403


def test_messages_and_moderation(client: TestClient, db_session, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    res = client.post("/api/v1/pitch/messages", json={"body": "来了"})
    assert res.status_code == 201, res.text
    message_id = res.json()["id"]

    assert client.get("/api/v1/pitch/messages").json()[0]["body"] == "来了"

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.post(
        f"/api/v1/admin/pitch/messages/{message_id}/moderation", json={"hidden": True}
    )
    assert res.status_code == 200
    assert client.get("/api/v1/pitch/messages").json() == []


def test_author_can_delete_own_message(client: TestClient, event, user, other_user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    message_id = client.post("/api/v1/pitch/messages", json={"body": "撤回我"}).json()["id"]
    client.delete("/api/v1/auth/sessions/current")

    login(client, "ana@example.com", "ana-password-123")
    assert client.delete(f"/api/v1/pitch/messages/{message_id}").status_code == 403

    login(client, "will@example.com", "correct-horse-battery")
    assert client.delete(f"/api/v1/pitch/messages/{message_id}").status_code == 200
