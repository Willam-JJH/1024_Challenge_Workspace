"""OAuth 授权码 + PKCE、用户信息与已连接应用。"""

import base64
import hashlib
import json

from fastapi.testclient import TestClient

from app.models.oauth import OAuthClient
from tests.conftest import login

VERIFIER = "verifier-1234567890abcdefghijklmnopqrstuvwxyz"
CHALLENGE = (
    base64.urlsafe_b64encode(hashlib.sha256(VERIFIER.encode()).digest())
    .decode()
    .rstrip("=")
)
REDIRECT = "http://localhost:9999/cb"


def _client(db) -> None:
    db.add(
        OAuthClient(
            name="Intern",
            client_id="intern-test",
            redirect_uris=json.dumps([REDIRECT]),
            scopes="profile llm",
        )
    )
    db.commit()


def test_full_authorization_code_flow(client: TestClient, db_session, event, user) -> None:
    _client(db_session)
    login(client, "will@example.com", "correct-horse-battery")

    info = client.get("/api/v1/oauth/consent", params={"client_id": "intern-test"}).json()
    assert info["approved"] is False

    res = client.get(
        "/api/v1/oauth/authorize",
        params={
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_challenge": CHALLENGE,
        },
        follow_redirects=False,
    )
    assert res.status_code == 403

    res = client.post(
        "/api/v1/oauth/consent",
        json={
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_challenge": CHALLENGE,
            "scope": "profile",
            "approve": True,
        },
    )
    redirect_url = res.json()["redirect_url"]
    code = redirect_url.split("code=")[1].split("&")[0]

    res = client.post(
        "/api/v1/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_verifier": VERIFIER,
        },
    )
    assert res.status_code == 200, res.text
    tokens = res.json()
    assert tokens["token_type"] == "Bearer"

    me = client.get(
        "/api/v1/oauth/userinfo", headers={"Authorization": f"Bearer {tokens['access_token']}"}
    )
    assert me.status_code == 200
    assert me.json()["email"] == "will@example.com"

    apps = client.get("/api/v1/me/connected-apps").json()
    assert apps[0]["client_name"] == "Intern"

    refreshed = client.post(
        "/api/v1/oauth/token",
        data={
            "grant_type": "refresh_token",
            "refresh_token": tokens["refresh_token"],
            "client_id": "intern-test",
        },
    ).json()
    assert refreshed["access_token"] != tokens["access_token"]

    old = client.get(
        "/api/v1/oauth/userinfo", headers={"Authorization": f"Bearer {tokens['access_token']}"}
    )
    assert old.status_code == 401


def test_wrong_verifier_and_revoke_device(client: TestClient, db_session, event, user) -> None:
    _client(db_session)
    login(client, "will@example.com", "correct-horse-battery")
    consent = client.post(
        "/api/v1/oauth/consent",
        json={
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_challenge": CHALLENGE,
            "approve": True,
        },
    ).json()
    code = consent["redirect_url"].split("code=")[1].split("&")[0]

    bad = client.post(
        "/api/v1/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_verifier": "wrong-verifier",
        },
    )
    assert bad.status_code == 400

    ok = client.post(
        "/api/v1/oauth/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "client_id": "intern-test",
            "redirect_uri": REDIRECT,
            "code_verifier": VERIFIER,
        },
    ).json()
    device_id = client.get("/api/v1/me/connected-apps").json()[0]["devices"][0]["device_id"]
    assert client.delete(
        f"/api/v1/me/connected-apps/intern-test/devices/{device_id}"
    ).status_code == 200
    assert client.get("/api/v1/me/connected-apps").json() == []

    revoked = client.post("/api/v1/oauth/revoke", json={"token": ok["access_token"]})
    assert revoked.status_code == 200


def test_usage_summary_and_metadata(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    summary = client.get("/api/v1/usage/summary").json()
    assert summary["requests"] == 0
    meta = client.get("/.well-known/oauth-authorization-server").json()
    assert meta["code_challenge_methods_supported"] == ["S256"]
