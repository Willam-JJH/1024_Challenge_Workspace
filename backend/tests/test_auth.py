"""健康检查、登录与会话。"""

from fastapi.testclient import TestClient


def test_health(client: TestClient) -> None:
    res = client.get("/api/v1/health")
    assert res.status_code == 200
    assert res.json()["status"] == "ok"


def test_login_sets_cookie_and_lists_session(client: TestClient, user) -> None:
    res = client.post(
        "/api/v1/auth/sessions",
        json={"email": "will@example.com", "password": "correct-horse-battery"},
    )
    assert res.status_code == 200, res.text
    assert res.cookies.get("sid")

    me = client.get("/api/v1/me")
    assert me.status_code == 200
    assert me.json()["email"] == "will@example.com"

    sessions = client.get("/api/v1/auth/sessions")
    assert sessions.status_code == 200
    assert len(sessions.json()) == 1


def test_login_rejects_bad_password(client: TestClient, user) -> None:
    res = client.post(
        "/api/v1/auth/sessions",
        json={"email": "will@example.com", "password": "wrong-password-123"},
    )
    assert res.status_code == 401
    assert res.json()["error"]["code"] == "unauthorized"


def test_me_requires_auth(client: TestClient) -> None:
    res = client.get("/api/v1/me")
    assert res.status_code == 401
    assert res.json()["error"]["code"] == "unauthorized"


def test_logout_revokes_session(client: TestClient, user) -> None:
    client.post(
        "/api/v1/auth/sessions",
        json={"email": "will@example.com", "password": "correct-horse-battery"},
    )
    assert client.delete("/api/v1/auth/sessions/current").status_code == 200
    assert client.get("/api/v1/me").status_code == 401


def test_stub_returns_contract_not_implemented(client: TestClient) -> None:
    res = client.post("/v1/embeddings", json={})
    assert res.status_code == 501
    body = res.json()
    assert body["error"]["code"] == "not_implemented"


def test_unknown_route_uses_error_envelope(client: TestClient) -> None:
    res = client.get("/api/v1/no-such-thing")
    assert res.status_code == 404
    assert "error" in res.json()
