"""展览：评论、广告、参观记录与介绍。"""

from fastapi.testclient import TestClient
from sqlalchemy.orm import Session

from app.models.event import Company, Membership
from app.models.exhibition import Introduction
from app.models.identity import User
from app.models.project import Project
from tests.conftest import login


def _company(db: Session, event, user: User, name: str, slug: str, role: str = "captain"):
    company = Company(event_id=event.id, name=name, slug=slug, captain_id=user.id)
    db.add(company)
    db.flush()
    db.add(Membership(company_id=company.id, user_id=user.id, role=role))
    db.commit()
    return company


def _published_project(db: Session, event, company: Company, title: str) -> Project:
    project = Project(
        event_id=event.id,
        company_id=company.id,
        status="published",
        title=title,
        content="正文",
        live_version=1,
    )
    db.add(project)
    db.commit()
    return project


def test_comment_lifecycle(client: TestClient, db_session, event, user, other_user) -> None:
    mine = _company(db_session, event, user, "Mine", "mine")
    project = _published_project(db_session, event, mine, "怪项目")

    login(client, "ana@example.com", "ana-password-123")
    res = client.post(
        f"/api/v1/exhibition/projects/{project.id}/comments", json={"body": "有点意思"}
    )
    assert res.status_code == 201, res.text
    comment_id = res.json()["id"]

    res = client.post(
        f"/api/v1/exhibition/projects/{project.id}/comments",
        json={"body": "谢谢", "parent_id": comment_id},
    )
    assert res.status_code == 201

    res = client.patch(f"/api/v1/exhibition/comments/{comment_id}", json={"body": "改一下"})
    assert res.status_code == 200
    assert res.json()["body"] == "改一下"

    res = client.post(f"/api/v1/exhibition/comments/{comment_id}/reports")
    assert res.status_code == 409  # 不能举报自己的评论

    login(client, "will@example.com", "correct-horse-battery")
    res = client.post(f"/api/v1/exhibition/comments/{comment_id}/reports")
    assert res.status_code == 200
    res = client.post(f"/api/v1/exhibition/comments/{comment_id}/reports")
    assert res.status_code == 409

    res = client.patch(f"/api/v1/exhibition/comments/{comment_id}", json={"body": "越权"})
    assert res.status_code == 403

    login(client, "ana@example.com", "ana-password-123")
    assert client.delete(f"/api/v1/exhibition/comments/{comment_id}").status_code == 200
    comments = client.get(f"/api/v1/exhibition/projects/{project.id}/comments").json()
    assert [c["body"] for c in comments] == ["谢谢"]


def test_promotion_limit(client: TestClient, db_session, event, user) -> None:
    _company(db_session, event, user, "Mine", "mine")
    login(client, "will@example.com", "correct-horse-battery")

    for i in range(3):
        res = client.post(
            "/api/v1/exhibition/promotions", json={"kind": "wall", "body": f"广告 {i}"}
        )
        assert res.status_code == 201, res.text
    res = client.post("/api/v1/exhibition/promotions", json={"kind": "wall", "body": "第四条"})
    assert res.status_code == 409

    listed = client.get("/api/v1/exhibition/promotions").json()
    assert len(listed) == 3


def test_passport_stamps(client: TestClient, db_session, event, user, other_user) -> None:
    mine = _company(db_session, event, user, "Mine", "mine")
    theirs = _company(db_session, event, other_user, "Theirs", "theirs")
    project = _published_project(db_session, event, mine, "怪项目")

    login(client, "will@example.com", "correct-horse-battery")
    res = client.put(f"/api/v1/exhibition/visits/{project.id}", json={"note": "自己"})
    assert res.status_code == 403

    login(client, "ana@example.com", "ana-password-123")
    res = client.put(f"/api/v1/exhibition/visits/{project.id}", json={"note": "好看"})
    assert res.status_code == 200

    passport = client.get("/api/v1/exhibition/visits").json()
    assert len(passport["items"]) == 1
    assert passport["complete"] is False
    assert passport["needed"] == 3

    res = client.delete(f"/api/v1/exhibition/visits/{project.id}")
    assert res.status_code == 200
    assert client.get("/api/v1/exhibition/visits").json()["items"] == []
    assert theirs.id != mine.id


def test_introductions_by_role(client: TestClient, db_session, event, user, other_user) -> None:
    mine = _company(db_session, event, user, "Mine", "mine")
    theirs = _company(db_session, event, other_user, "Theirs", "theirs")
    intro = Introduction(
        event_id=event.id, introducer_company_id=mine.id, target_company_id=theirs.id
    )
    db_session.add(intro)
    db_session.commit()

    login(client, "will@example.com", "correct-horse-battery")
    res = client.put(
        f"/api/v1/exhibition/introductions/{intro.id}/text", json={"text": "他们很怪"}
    )
    assert res.status_code == 200
    res = client.put(
        f"/api/v1/exhibition/introductions/{intro.id}/reply", json={"reply": "确实"}
    )
    assert res.status_code == 403

    login(client, "ana@example.com", "ana-password-123")
    res = client.put(
        f"/api/v1/exhibition/introductions/{intro.id}/reply", json={"reply": "确实很怪"}
    )
    assert res.status_code == 200
    assert res.json()["role"] == "target"
    assert res.json()["reply"] == "确实很怪"


def test_feed_lists_projects_and_promotions(
    client: TestClient, db_session, event, user
) -> None:
    mine = _company(db_session, event, user, "Mine", "mine")
    _published_project(db_session, event, mine, "上架作品")
    login(client, "will@example.com", "correct-horse-battery")
    client.post("/api/v1/exhibition/promotions", json={"kind": "ticker", "body": "看这里"})

    feed = client.get("/api/v1/exhibition/feed").json()
    types = {item["type"] for item in feed}
    assert types == {"project", "promotion"}
