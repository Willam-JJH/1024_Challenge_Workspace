"""公司：组建、邀请、入队申请与权限边界。"""

from fastapi.testclient import TestClient

from app.models.identity import User
from tests.conftest import login


def _create_company(client: TestClient, name: str = "Blue Fox") -> dict:
    res = client.post("/api/v1/companies", json={"name": name, "blurb": "我们做点怪东西"})
    assert res.status_code == 201, res.text
    return res.json()


def test_create_company_makes_creator_captain(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)
    assert company["members"][0]["role"] == "captain"
    assert company["members"][0]["user_id"] == user.id
    assert company["seats_left"] == event.company_seats - 1


def test_one_company_per_person(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    _create_company(client)
    res = client.post("/api/v1/companies", json={"name": "Second Try"})
    assert res.status_code == 409


def test_invitation_and_acceptance(client: TestClient, event, user, other_user, mailer) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)

    res = client.post(
        f"/api/v1/companies/{company['id']}/invitations", json={"email": "ana@example.com"}
    )
    assert res.status_code == 201, res.text
    invitation = res.json()["invitation"]
    token = res.json()["token"]
    assert token
    assert mailer.notices

    preview = client.post("/api/v1/invitations/previews", json={"token": token})
    assert preview.status_code == 200
    assert preview.json()["company_name"] == "Blue Fox"

    client.delete("/api/v1/auth/sessions/current")
    login(client, "ana@example.com", "ana-password-123")
    res = client.post(f"/api/v1/invitations/{invitation['id']}/acceptance")
    assert res.status_code == 200, res.text

    detail = client.get(f"/api/v1/companies/{company['id']}").json()
    assert len(detail["members"]) == 2


def test_captain_can_transfer(client: TestClient, event, user, other_user, mailer) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)
    res = client.post(
        f"/api/v1/companies/{company['id']}/invitations", json={"email": "ana@example.com"}
    )
    invitation = res.json()["invitation"]
    client.delete("/api/v1/auth/sessions/current")
    login(client, "ana@example.com", "ana-password-123")
    client.post(f"/api/v1/invitations/{invitation['id']}/acceptance")

    login(client, "will@example.com", "correct-horse-battery")
    res = client.post(
        f"/api/v1/companies/{company['id']}/captain-transfers", json={"user_id": other_user.id}
    )
    assert res.status_code == 200

    detail = client.get(f"/api/v1/companies/{company['id']}").json()
    roles = {m["user_id"]: m["role"] for m in detail["members"]}
    assert roles[other_user.id] == "captain"
    assert roles[user.id] == "member"


def test_join_request_flow(client: TestClient, event, user, other_user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)
    client.delete("/api/v1/auth/sessions/current")

    login(client, "ana@example.com", "ana-password-123")
    res = client.post(
        f"/api/v1/companies/{company['id']}/join-requests", json={"message": "拉我一把"}
    )
    assert res.status_code == 201, res.text
    request_id = res.json()["id"]
    client.delete("/api/v1/auth/sessions/current")

    login(client, "will@example.com", "correct-horse-battery")
    res = client.post(f"/api/v1/companies/{company['id']}/join-requests/{request_id}/acceptance")
    assert res.status_code == 200, res.text

    detail = client.get(f"/api/v1/companies/{company['id']}").json()
    assert len(detail["members"]) == 2


def test_full_company_rejects_join(db_session, client: TestClient, event, user, other_user) -> None:
    event.company_seats = 1
    db_session.add(event)
    db_session.commit()

    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)
    client.delete("/api/v1/auth/sessions/current")

    login(client, "ana@example.com", "ana-password-123")
    res = client.post(f"/api/v1/companies/{company['id']}/join-requests", json={})
    assert res.status_code == 409
    assert "已满" in res.json()["error"]["message"]


def test_non_member_cannot_invite(client: TestClient, event, user, other_user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    company = _create_company(client)
    client.delete("/api/v1/auth/sessions/current")

    login(client, "ana@example.com", "ana-password-123")
    res = client.post(
        f"/api/v1/companies/{company['id']}/invitations", json={"email": "x@example.com"}
    )
    assert res.status_code == 403


def test_api_key_authenticates(client: TestClient, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    res = client.post("/api/v1/me/api-keys", json={"label": "laptop"})
    assert res.status_code == 201, res.text
    token = res.json()["token"]
    key_id = res.json()["key"]["id"]

    res = client.get("/api/v1/me", headers={"Authorization": f"Bearer {token}"})
    assert res.status_code == 200
    assert res.json()["email"] == "will@example.com"

    client.delete(f"/api/v1/me/api-keys/{key_id}")
    res = client.get("/api/v1/me", headers={"Authorization": f"Bearer {token}"})
    assert res.status_code == 401


__all__ = ["User"]
