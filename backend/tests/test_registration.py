"""注册、激活与口令重置。"""

from fastapi.testclient import TestClient

from tests.conftest import login


def test_register_verify_and_sign_in(client: TestClient, mailer) -> None:
    res = client.post(
        "/api/v1/auth/registrations",
        json={
            "email": "new@example.com",
            "password": "brand-new-password",
            "display_name": "Newcomer",
        },
    )
    assert res.status_code == 202, res.text

    code = mailer.last_code("register")
    res = client.post(
        "/api/v1/auth/registrations/verifications",
        json={"email": "new@example.com", "code": code},
    )
    assert res.status_code == 200, res.text
    assert res.cookies.get("sid")

    me = client.get("/api/v1/me")
    assert me.status_code == 200
    assert me.json()["email"] == "new@example.com"
    assert me.json()["status"] == "active"


def test_wrong_code_is_rejected(client: TestClient, mailer) -> None:
    client.post(
        "/api/v1/auth/registrations",
        json={"email": "typo@example.com", "password": "another-password", "display_name": "T"},
    )
    res = client.post(
        "/api/v1/auth/registrations/verifications",
        json={"email": "typo@example.com", "code": "000000"},
    )
    assert res.status_code == 422
    assert res.json()["error"]["code"] == "unprocessable"


def test_duplicate_registration_conflicts(client: TestClient, mailer, user) -> None:
    res = client.post(
        "/api/v1/auth/registrations",
        json={
            "email": "will@example.com",
            "password": "whatever-password",
            "display_name": "Will again",
        },
    )
    assert res.status_code == 409


def test_password_reset_flow(client: TestClient, mailer, user) -> None:
    res = client.post(
        "/api/v1/auth/password-reset-requests", json={"email": "will@example.com"}
    )
    assert res.status_code == 200

    code = mailer.last_code("password_reset")
    res = client.post(
        "/api/v1/auth/password-reset-grants",
        json={"email": "will@example.com", "code": code},
    )
    assert res.status_code == 200, res.text
    grant = res.json()["token"]

    res = client.post(
        "/api/v1/auth/password-resets",
        json={"token": grant, "new_password": "reset-password-456"},
    )
    assert res.status_code == 200, res.text

    login(client, "will@example.com", "reset-password-456")


def test_unknown_email_does_not_leak(client: TestClient, mailer) -> None:
    res = client.post(
        "/api/v1/auth/password-reset-requests", json={"email": "ghost@example.com"}
    )
    assert res.status_code == 200
    assert mailer.codes == []
