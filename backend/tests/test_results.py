"""结果与评审：打分、入围名单与奖项。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.event import Company, Membership
from app.models.identity import User
from app.models.project import Project
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


def _setup(db: Session, event, user, other_user) -> tuple[Project, Project]:
    first = Company(event_id=event.id, name="First", slug="first", captain_id=user.id)
    second = Company(event_id=event.id, name="Second", slug="second", captain_id=other_user.id)
    db.add_all([first, second])
    db.flush()
    db.add_all(
        [
            Membership(company_id=first.id, user_id=user.id, role="captain"),
            Membership(company_id=second.id, user_id=other_user.id, role="captain"),
        ]
    )
    p1 = Project(
        event_id=event.id, company_id=first.id, status="published", title="作品一", live_version=1
    )
    p2 = Project(
        event_id=event.id, company_id=second.id, status="published", title="作品二", live_version=1
    )
    db.add_all([p1, p2])
    db.flush()
    # 作品一所在公司拿到两票，排名第一
    db.add_all(
        [
            Vote(event_id=event.id, voter_id=other_user.id, company_id=first.id),
            Vote(event_id=event.id, voter_id=user.id, company_id=second.id),
            Vote(event_id=event.id, voter_id=user.id, company_id=first.id),
        ]
    )
    db.commit()
    return p1, p2


def test_only_staff_can_judge(client: TestClient, db_session, event, user, other_user) -> None:
    p1, _ = _setup(db_session, event, user, other_user)

    login(client, "will@example.com", "correct-horse-battery")
    res = client.put(
        f"/api/v1/judging/projects/{p1.id}/scores",
        json={"creativity": 8, "engineering": 8, "presentation": 8},
    )
    assert res.status_code == 403

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.put(
        f"/api/v1/judging/projects/{p1.id}/scores",
        json={"creativity": 9, "engineering": 7, "presentation": 8, "submit": True},
    )
    assert res.status_code == 200, res.text
    assert res.json()["submitted_at"] is not None

    projects = client.get("/api/v1/judging/projects").json()
    mine = next(item for item in projects if item["project"]["id"] == p1.id)
    assert mine["my_score"]["creativity"] == 9


def test_shortlist_advances_by_coins(
    client: TestClient, db_session, event, user, other_user
) -> None:
    p1, p2 = _setup(db_session, event, user, other_user)
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    res = client.post("/api/v1/admin/results/shortlist", json={"top_n": 1})
    assert res.status_code == 200, res.text
    assert [row["project_id"] for row in res.json()] == [p1.id]

    public = client.get("/api/v1/results/shortlist").json()
    assert public[0]["company_name"] == "First"

    res = client.delete("/api/v1/admin/results/shortlist")
    assert res.status_code == 200
    assert client.get("/api/v1/results/shortlist").json() == []
    assert p2.id != p1.id


def test_awards_and_results(client: TestClient, db_session, event, user, other_user) -> None:
    p1, _ = _setup(db_session, event, user, other_user)

    results = client.get("/api/v1/results").json()
    assert results["announced"] is False
    assert results["awards"] == []

    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    res = client.post(
        "/api/v1/admin/results/awards",
        json={"kind": "first", "title": "一等奖", "project_id": p1.id},
    )
    assert res.status_code == 201, res.text
    award_id = res.json()["id"]

    res = client.post(
        "/api/v1/admin/results/awards",
        json={"kind": "first", "title": "重复", "project_id": p1.id},
    )
    assert res.status_code == 409

    results = client.get("/api/v1/results").json()
    assert results["announced"] is True
    assert results["awards"][0]["kind"] == "first"
    assert results["board"][0]["name"] == "First"

    assert client.delete(f"/api/v1/admin/results/awards/{award_id}").status_code == 200
    assert client.get("/api/v1/results").json()["announced"] is False
