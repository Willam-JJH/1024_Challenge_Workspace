"""管理台：公司管理、投票管理。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.event import Company, Membership
from app.models.identity import User
from app.models.vote import Vote
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


def _companies(db: Session, event, user, other_user) -> tuple[Company, Company]:
    first = Company(event_id=event.id, name="First", slug="first", captain_id=user.id)
    second = Company(event_id=event.id, name="Second", slug="second")
    db.add_all([first, second])
    db.flush()
    db.add(Membership(company_id=first.id, user_id=user.id, role="captain"))
    db.commit()
    return first, second


def test_company_administration(client: TestClient, db_session, event, user, other_user) -> None:
    first, second = _companies(db_session, event, user, other_user)
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    rows = client.get("/api/v1/admin/companies").json()
    assert {row["slug"] for row in rows} == {"first", "second"}

    res = client.put(f"/api/v1/admin/companies/{first.id}/roster-lock", json={"locked": True})
    assert res.status_code == 200

    res = client.post(
        f"/api/v1/admin/companies/{first.id}/dissolutions"
    )
    assert res.status_code == 200
    detail = client.get(f"/api/v1/admin/companies/{first.id}").json()
    assert detail["members"] == []


def test_move_member_between_companies(
    client: TestClient, db_session, event, user, other_user
) -> None:
    first, second = _companies(db_session, event, user, other_user)
    db_session.add(Membership(company_id=first.id, user_id=other_user.id, role="member"))
    db_session.commit()

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.post(
        "/api/v1/admin/members/moves",
        json={
            "user_id": other_user.id,
            "from_company_id": first.id,
            "to_company_id": second.id,
        },
    )
    assert res.status_code == 200, res.text

    groups = client.get(f"/api/v1/admin/companies/{second.id}").json()["members"]
    assert [row["user_id"] for row in groups] == [other_user.id]


def test_transfer_captain(client: TestClient, db_session, event, user, other_user) -> None:
    first, _ = _companies(db_session, event, user, other_user)
    db_session.add(Membership(company_id=first.id, user_id=other_user.id, role="member"))
    db_session.commit()

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.post(
        f"/api/v1/admin/companies/{first.id}/captain-transfers",
        json={"user_id": other_user.id},
    )
    assert res.status_code == 200

    detail = client.get(f"/api/v1/admin/companies/{first.id}").json()
    roles = {row["user_id"]: row["role"] for row in detail["members"]}
    assert roles[other_user.id] == "captain"
    assert roles[user.id] == "member"


def test_voting_admin_and_visibility(
    client: TestClient, db_session, event, user, other_user
) -> None:
    first, second = _companies(db_session, event, user, other_user)
    db_session.add(
        Vote(event_id=event.id, voter_id=other_user.id, company_id=first.id, status="live")
    )
    db_session.commit()

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    state = client.get("/api/v1/admin/voting").json()
    assert state["coins_total"] == 1
    ballots = client.get("/api/v1/admin/voting/ballots").json()
    assert ballots[0]["company_name"] == "First"

    res = client.put("/api/v1/admin/voting/visibility", json={"visible": False})
    assert res.status_code == 200
    public = client.get("/api/v1/votes").json()
    assert public["total_coins"] == 0
    assert all(row["coins"] == 0 for row in public["items"])

    res = client.post("/api/v1/admin/voting/voids", json={"company_id": first.id})
    assert res.status_code == 200
    ballots = client.get(
        "/api/v1/admin/voting/ballots", params={"status": "live"}
    ).json()
    assert ballots == []
    assert second.id != first.id


def test_admin_operations_require_staff(
    client: TestClient, db_session, event, user, other_user
) -> None:
    first, _ = _companies(db_session, event, user, other_user)
    login(client, "will@example.com", "correct-horse-battery")
    assert client.get("/api/v1/admin/voting/ballots").status_code == 403
    assert client.put(
        f"/api/v1/admin/companies/{first.id}/roster-lock", json={"locked": True}
    ).status_code == 403
