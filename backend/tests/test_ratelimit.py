"""限流：滑动窗口单测 + 登录、发码、校验的 429 行为。"""

import time
from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient

from app.core.config import settings
from app.core.ratelimit import SlidingWindow, limiter


@pytest.fixture()
def limits_on(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    monkeypatch.setattr(settings, "rate_limit_enabled", True)
    limiter.reset()
    yield
    limiter.reset()


def test_window_allows_up_to_limit() -> None:
    window = SlidingWindow()
    for _ in range(3):
        ok, retry = window.allow("k", 3, 60)
        assert ok and retry == 0

    ok, retry = window.allow("k", 3, 60)
    assert not ok
    assert 1 <= retry <= 60

    # 不同 key 互不影响
    assert window.allow("other", 3, 60)[0]


def test_window_expires() -> None:
    window = SlidingWindow()
    assert window.allow("k", 1, 0.05)[0]
    assert not window.allow("k", 1, 0.05)[0]
    time.sleep(0.06)
    assert window.allow("k", 1, 0.05)[0]


def test_window_disabled_when_limit_is_zero() -> None:
    window = SlidingWindow()
    for _ in range(5):
        assert window.allow("k", 0, 60)[0]


def test_login_failures_lock_out(
    client: TestClient, limits_on: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(settings, "rate_limit_login_pair", 2)
    payload = {"email": "locked@example.com", "password": "wrong-password"}

    for _ in range(2):
        assert client.post("/api/v1/auth/sessions", json=payload).status_code == 401

    res = client.post("/api/v1/auth/sessions", json=payload)
    assert res.status_code == 429, res.text
    assert res.json()["error"]["code"] == "rate_limited"
    assert res.headers["Retry-After"].isdigit()

    # 换一个邮箱不受影响
    other = {"email": "someone-else@example.com", "password": "wrong-password"}
    assert client.post("/api/v1/auth/sessions", json=other).status_code == 401


def test_code_send_cooldown(client: TestClient, limits_on: None, mailer) -> None:
    payload = {
        "email": "cooldown@example.com",
        "password": "cooldown-password",
        "display_name": "Cooldown",
    }
    assert client.post("/api/v1/auth/registrations", json=payload).status_code == 202

    res = client.post("/api/v1/auth/registrations", json=payload)
    assert res.status_code == 429, res.text
    assert 1 <= int(res.headers["Retry-After"]) <= 60


def test_verify_attempts_limited(
    client: TestClient, limits_on: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(settings, "rate_limit_verify_email", 2)
    payload = {"email": "nobody@example.com", "code": "000000"}

    for _ in range(2):
        assert client.post(
            "/api/v1/auth/registrations/verifications", json=payload
        ).status_code == 404

    res = client.post("/api/v1/auth/registrations/verifications", json=payload)
    assert res.status_code == 429, res.text
    assert res.headers["Retry-After"].isdigit()
