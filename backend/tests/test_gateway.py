"""网关：路由解析、熔断、预算与计量。"""

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.llm import LlmModel, LlmRoute, LlmUpstream, LlmUsage
from app.services import gateway
from tests.conftest import login


def _api_key(client: TestClient, email: str = "will@example.com") -> str:
    login(client, email, "correct-horse-battery")
    res = client.post("/api/v1/me/api-keys", json={"label": "gateway"})
    assert res.status_code == 201, res.text
    client.delete("/api/v1/auth/sessions/current")
    return res.json()["token"]


def _wire(db: Session) -> LlmModel:
    upstream = LlmUpstream(name="new-api", base_url="http://localhost:3000", api_key="sk-test")
    db.add(upstream)
    db.flush()
    model = LlmModel(
        name="qwen3-coder",
        input_price_micro=1_000_000,
        output_price_micro=2_000_000,
        context_window=44000,
    )
    db.add(model)
    db.flush()
    db.add(
        LlmRoute(model_id=model.id, upstream_id=upstream.id, upstream_model="qwen3-coder")
    )
    db.commit()
    return model


def test_models_requires_auth(client: TestClient, event) -> None:
    assert client.get("/v1/models").status_code == 401


def test_models_lists_enabled(client: TestClient, db_session, event, user) -> None:
    _wire(db_session)
    LlmModel(name="disabled", enabled=False)
    db_session.add(LlmModel(name="disabled", enabled=False))
    db_session.commit()
    token = _api_key(client)

    data = client.get("/v1/models", headers={"Authorization": f"Bearer {token}"}).json()
    assert [row["id"] for row in data["data"]] == ["qwen3-coder"]


def test_completion_proxies_and_meters(
    client: TestClient, db_session, event, user, monkeypatch
) -> None:
    _wire(db_session)
    token = _api_key(client)

    def fake_post(url: str, headers: dict, payload: dict) -> tuple[int, dict]:
        assert url == "http://localhost:3000/v1/chat/completions"
        assert headers["Authorization"] == "Bearer sk-test"
        assert payload["model"] == "qwen3-coder"
        return 200, {
            "choices": [{"message": {"role": "assistant", "content": "hi"}}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 20},
        }

    monkeypatch.setattr(gateway, "_http_post", fake_post)
    res = client.post(
        "/v1/chat/completions",
        headers={"Authorization": f"Bearer {token}"},
        json={"model": "qwen3-coder", "messages": [{"role": "user", "content": "hi"}]},
    )
    assert res.status_code == 200, res.text
    assert res.json()["choices"][0]["message"]["content"] == "hi"

    usage = db_session.scalars(select(LlmUsage)).one()
    assert usage.prompt_tokens == 10
    assert usage.completion_tokens == 20
    assert usage.cost_micro == 50


def test_kill_switch_and_budget(client: TestClient, db_session, event, user, monkeypatch) -> None:
    _wire(db_session)
    token = _api_key(client)
    monkeypatch.setattr(gateway, "_http_post", lambda *a, **k: (200, {"usage": {}}))

    event.llm_kill_switch = True
    db_session.add(event)
    db_session.commit()
    res = client.post(
        "/v1/chat/completions",
        headers={"Authorization": f"Bearer {token}"},
        json={"model": "qwen3-coder", "messages": []},
    )
    assert res.status_code == 503

    event.llm_kill_switch = False
    event.llm_budget_micro = 10
    db_session.add(event)
    db_session.commit()
    db_session.add(LlmUsage(user_id=user.id, prompt_tokens=1, completion_tokens=1, cost_micro=10))
    db_session.commit()
    res = client.post(
        "/v1/chat/completions",
        headers={"Authorization": f"Bearer {token}"},
        json={"model": "qwen3-coder", "messages": []},
    )
    assert res.status_code == 402


def test_unknown_model_and_stream(client: TestClient, db_session, event, user) -> None:
    token = _api_key(client)
    res = client.post(
        "/v1/chat/completions",
        headers={"Authorization": f"Bearer {token}"},
        json={"model": "nope", "messages": []},
    )
    assert res.status_code == 404

    res = client.post(
        "/v1/chat/completions",
        headers={"Authorization": f"Bearer {token}"},
        json={"model": "nope", "messages": [], "stream": True},
    )
    assert res.status_code == 501


@pytest.mark.parametrize("path", ["/v1/embeddings", "/v1/responses", "/v1/search"])
def test_reserved_endpoints(client: TestClient, event, path: str) -> None:
    assert client.post(path, json={}).status_code == 501
