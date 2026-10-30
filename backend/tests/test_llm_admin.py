"""LLM 管理台：上游、模型、路由、预算、熔断与用量。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.event import Company, Membership
from app.models.identity import User
from app.models.llm import LlmUsage
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


def test_upstream_model_route_and_budget(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    upstream = client.post(
        "/api/v1/admin/llm/upstreams",
        json={"name": "new-api", "base_url": "http://localhost:3000", "api_key": "sk-test"},
    ).json()
    assert upstream["has_key"] is True
    assert "api_key" not in upstream

    model = client.post(
        "/api/v1/admin/llm/models",
        json={"name": "qwen3-coder", "input_price_micro": 100, "output_price_micro": 300},
    ).json()

    route = client.post(
        f"/api/v1/admin/llm/models/{model['id']}/routes",
        json={"upstream_id": upstream["id"], "upstream_model": "qwen3-coder"},
    )
    assert route.status_code == 201, route.text

    res = client.put("/api/v1/admin/llm/budget", json={"budget_micro": 1_000_000})
    assert res.json()["remaining_micro"] == 1_000_000

    overview = client.get("/api/v1/admin/llm/overview").json()
    assert overview["models"] == 1
    assert overview["upstreams"] == 1


def test_kill_switches(client: TestClient, db_session, event, user) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    model = client.post("/api/v1/admin/llm/models", json={"name": "m1"}).json()

    client.put("/api/v1/admin/llm/kill-switches/global", json={"enabled": False})
    client.put(f"/api/v1/admin/llm/kill-switches/models/{model['id']}", json={"enabled": False})
    client.put(f"/api/v1/admin/llm/kill-switches/users/{user.id}", json={"enabled": False})

    state = client.get("/api/v1/admin/llm/kill-switches").json()
    assert state["global"] is True
    assert state["disabled_models"] == [model["id"]]
    assert state["blocked_users"] == [user.id]


def test_usage_aggregation(client: TestClient, db_session, event, user, other_user) -> None:
    company = Company(event_id=event.id, name="Fox", slug="fox")
    db_session.add(company)
    db_session.flush()
    db_session.add(Membership(company_id=company.id, user_id=user.id, role="captain"))
    db_session.add(
        LlmUsage(user_id=user.id, prompt_tokens=1000, completion_tokens=500, cost_micro=250)
    )
    db_session.add(
        LlmUsage(user_id=other_user.id, prompt_tokens=10, completion_tokens=5, cost_micro=3)
    )
    db_session.commit()

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    users = client.get("/api/v1/admin/llm/usage/users").json()
    assert users[0]["label"] == "Will"
    assert users[0]["cost_micro"] == 250

    teams = client.get("/api/v1/admin/llm/usage/teams").json()
    assert teams[0]["label"] == "Fox"


def test_llm_admin_requires_staff(client: TestClient, event, user) -> None:
    login(client, "will@example.com", "correct-horse-battery")
    assert client.get("/api/v1/admin/llm/overview").status_code == 403
