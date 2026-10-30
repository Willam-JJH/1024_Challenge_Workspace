"""个人中心与系统小接口。"""

from fastapi.testclient import TestClient

from tests.conftest import login


def test_membership_and_participation(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    assert client.get("/api/v1/me/membership").json()["membership"] is None

    res = client.put("/api/v1/me/participation", json={"mode": "participant"})
    assert res.status_code == 200
    assert client.get("/api/v1/me").json()["role"] == "member"

    res = client.put("/api/v1/me/participation", json={"mode": "audience"})
    assert res.status_code == 200
    assert client.get("/api/v1/me").json()["role"] == "participant"


def test_digest_notifications_and_deletion(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")

    assert client.get("/api/v1/me/digest-preferences").json()["comments"] is True
    res = client.put("/api/v1/me/digest-preferences", json={"comments": False})
    assert res.json()["comments"] is False

    assert client.get("/api/v1/me/notifications").json() == []
    assert client.post("/api/v1/me/notifications/read").status_code == 200

    res = client.post("/api/v1/me/deletion-request")
    assert res.status_code == 200
    assert res.json()["requested_at"] is not None
    res = client.delete("/api/v1/me/deletion-request")
    assert res.json()["requested_at"] is None

    passport = client.get("/api/v1/me/passport").json()
    assert passport["items"] == [] and passport["needed"] == 3


def test_csp_report_and_link_preview_guard(client: TestClient, event, user) -> None:
    res = client.post(
        "/api/v1/csp-reports", json={"report": {"blocked-uri": "https://evil.example"}}
    )
    assert res.status_code == 204

    login(client, "will@example.com", "correct-horse-battery")
    res = client.post("/api/v1/link-previews", json={"url": "http://127.0.0.1:3000"})
    assert res.status_code == 403
    res = client.post("/api/v1/link-previews", json={"url": "ftp://example.com"})
    assert res.status_code == 422
