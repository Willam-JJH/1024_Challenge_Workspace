"""管理台：介绍任务、审核与签到。"""

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.event import Company, Membership
from app.models.exhibition import Comment, CommentReport
from app.models.identity import User
from app.models.project import Project
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


def _world(db: Session, event, user, other_user) -> list[Company]:
    companies = []
    for name, slug, owner in (("A", "a", user), ("B", "b", other_user)):
        company = Company(event_id=event.id, name=name, slug=slug, captain_id=owner.id)
        db.add(company)
        db.flush()
        db.add(Membership(company_id=company.id, user_id=owner.id, role="captain"))
        companies.append(company)
    db.commit()
    return companies


def test_generate_and_publish(client: TestClient, db_session, event, user, other_user) -> None:
    _world(db_session, event, user, other_user)
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    generated = client.post("/api/v1/admin/exhibition/assignments/generation").json()
    assert len(generated) == 2
    assert generated[0]["introducer_company"] != generated[0]["target_company"]

    res = client.post("/api/v1/admin/exhibition/assignments/publications")
    assert res.status_code == 200
    listed = client.get("/api/v1/admin/exhibition/assignments").json()
    assert all(row["status"] == "published" for row in listed)

    client.post("/api/v1/admin/exhibition/assignments/reveal-all")
    listed = client.get("/api/v1/admin/exhibition/assignments").json()
    assert all(row["given_at"] for row in listed)


def test_comment_audit_and_moderation(
    client: TestClient, db_session, event, user, other_user
) -> None:
    companies = _world(db_session, event, user, other_user)
    project = Project(
        event_id=event.id, company_id=companies[0].id, status="published",
        title="作品",
        live_version=1,
    )
    db_session.add(project)
    db_session.flush()
    comment = Comment(project_id=project.id, author_id=other_user.id, body="有点意思")
    db_session.add(comment)
    db_session.flush()
    db_session.add(CommentReport(comment_id=comment.id, reporter_id=user.id))

    intro = client  # noqa: F841
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    comments = client.get("/api/v1/admin/exhibition/comments").json()
    assert comments[0]["reports"] == 1

    res = client.put(f"/api/v1/admin/exhibition/presence/{companies[0].id}", json={"present": True})
    assert res.status_code == 200
    db_session.expire_all()
    assert db_session.get(Company, companies[0].id).present is True


def test_assignment_moderation(client: TestClient, db_session, event, user, other_user) -> None:
    _world(db_session, event, user, other_user)
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")

    generated = client.post("/api/v1/admin/exhibition/assignments/generation").json()
    first = generated[0]
    res = client.post(
        f"/api/v1/admin/exhibition/introductions/{first['id']}/moderation",
        json={"hidden": True},
    )
    assert res.status_code == 200
    listed = client.get("/api/v1/admin/exhibition/assignments").json()
    assert next(row for row in listed if row["id"] == first["id"])["status"] == "hidden"

    overview = client.get("/api/v1/admin/exhibition").json()
    assert overview["companies"] == 2
