"""投票：投票记录、上限、自投与榜单排名。"""

from fastapi.testclient import TestClient
from sqlalchemy.orm import Session

from app.models.event import Company, Membership
from app.models.identity import User
from tests.conftest import login


def _company(db: Session, event, name: str, slug: str) -> Company:
    company = Company(event_id=event.id, name=name, slug=slug)
    db.add(company)
    db.commit()
    return company


def _join(db: Session, company: Company, user: User, role: str = "member") -> None:
    db.add(Membership(company_id=company.id, user_id=user.id, role=role))
    db.commit()


def test_cannot_invest_in_own_company(client: TestClient, db_session, event, user) -> None:
    company = _company(db_session, event, "Mine", "mine")
    _join(db_session, company, user, role="captain")
    login(client, "will@example.com", "correct-horse-battery")
    res = client.put(f"/api/v1/me/votes/{company.id}")
    assert res.status_code == 403


def test_my_votes_caps_at_three(client: TestClient, db_session, event, user, other_user) -> None:
    companies = [_company(db_session, event, f"C{i}", f"c{i}") for i in range(4)]
    _join(db_session, companies[0], user, role="captain")

    login(client, "ana@example.com", "ana-password-123")
    for company in companies[1:4]:
        res = client.put(f"/api/v1/me/votes/{company.id}")
        assert res.status_code == 200, res.text
    assert res.json()["votes_left"] == 0

    res = client.put(f"/api/v1/me/votes/{companies[0].id}")
    assert res.status_code == 409


def test_withdraw_is_idempotent(client: TestClient, db_session, event, user, other_user) -> None:
    company = _company(db_session, event, "Target", "target")
    login(client, "ana@example.com", "ana-password-123")

    client.put(f"/api/v1/me/votes/{company.id}")
    res = client.delete(f"/api/v1/me/votes/{company.id}")
    assert res.status_code == 200
    assert res.json()["votes"] == []
    res = client.delete(f"/api/v1/me/votes/{company.id}")
    assert res.status_code == 200


def test_replace_my_votes(client: TestClient, db_session, event, user, other_user) -> None:
    companies = [_company(db_session, event, f"R{i}", f"r{i}") for i in range(3)]
    login(client, "ana@example.com", "ana-password-123")

    res = client.put(
        "/api/v1/me/votes", json={"company_ids": [companies[0].id, companies[1].id]}
    )
    assert res.status_code == 200
    assert {p["company_id"] for p in res.json()["votes"]} == {
        companies[0].id,
        companies[1].id,
    }

    res = client.put("/api/v1/me/votes", json={"company_ids": [companies[2].id]})
    assert [p["company_id"] for p in res.json()["votes"]] == [companies[2].id]


def test_board_ranks_with_ties(client: TestClient, db_session, event, user, other_user) -> None:
    first = _company(db_session, event, "First", "first")
    second = _company(db_session, event, "Second", "second")
    _company(db_session, event, "Third", "third")

    carl = User(
        email="carl@example.com",
        password_hash=user.password_hash,
        display_name="Carl",
        role="participant",
    )
    db_session.add(carl)
    db_session.commit()

    login(client, "ana@example.com", "ana-password-123")
    client.put(f"/api/v1/me/votes/{first.id}")
    client.put(f"/api/v1/me/votes/{second.id}")
    client.delete("/api/v1/auth/sessions/current")

    # 另一个投票人给 First 再投一枚
    from app.core.security import hash_password

    carl.password_hash = hash_password("carl-password-123")
    db_session.add(carl)
    db_session.commit()
    login(client, "carl@example.com", "carl-password-123")
    client.put(f"/api/v1/me/votes/{first.id}")

    board = client.get("/api/v1/votes").json()
    ranks = {row["slug"]: (row["votes"], row["rank"]) for row in board["items"]}
    assert ranks["first"] == (2, 1)
    assert ranks["second"] == (1, 2)
    assert ranks["third"] == (0, 3)
    assert board["total_votes"] == 3


def test_voting_closed_blocks_invest(
    client: TestClient, db_session, event, user, other_user
) -> None:
    company = _company(db_session, event, "Closed", "closed")
    event.voting_open = False
    db_session.add(event)
    db_session.commit()

    login(client, "ana@example.com", "ana-password-123")
    res = client.put(f"/api/v1/me/votes/{company.id}")
    assert res.status_code == 403
