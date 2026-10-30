"""作品：草稿、编辑锁、发布、版本与出席。"""

from fastapi.testclient import TestClient

from tests.conftest import login


def _setup_company(client: TestClient, name: str = "Blue Fox") -> dict:
    login(client, "will@example.com", "correct-horse-battery")
    res = client.post("/api/v1/companies", json={"name": name})
    assert res.status_code == 201, res.text
    return res.json()


def _lock(client: TestClient, project_id: str) -> str:
    res = client.post(
        f"/api/v1/projects/{project_id}/edit-sessions", json={"take_over": False}
    )
    assert res.status_code == 200, res.text
    return res.json()["token"]


def test_project_is_created_on_first_access(client: TestClient, event, user) -> None:
    _setup_company(client)
    res = client.get("/api/v1/projects/mine")
    assert res.status_code == 200, res.text
    assert res.json()["status"] == "draft"
    assert res.json()["revision"] == 0


def test_draft_save_requires_lock_and_revision(client: TestClient, event, user) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()

    res = client.patch(
        f"/api/v1/projects/{project['id']}/draft",
        json={"base_revision": 0, "title": "No lock"},
    )
    assert res.status_code == 409

    token = _lock(client, project["id"])
    res = client.patch(
        f"/api/v1/projects/{project['id']}/draft",
        json={"base_revision": 0, "title": "第一版", "summary": "摘要", "content": "# hi"},
        headers={"X-Edit-Token": token},
    )
    assert res.status_code == 200, res.text
    assert res.json()["revision"] == 1

    res = client.patch(
        f"/api/v1/projects/{project['id']}/draft",
        json={"base_revision": 0, "title": "旧修订"},
        headers={"X-Edit-Token": token},
    )
    assert res.status_code == 409
    assert "修订号" in res.json()["error"]["message"]


def test_publish_and_versions(client: TestClient, event, user) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()
    token = _lock(client, project["id"])
    client.patch(
        f"/api/v1/projects/{project['id']}/draft",
        json={"base_revision": 0, "title": "怪公司", "content": "正文"},
        headers={"X-Edit-Token": token},
    )

    res = client.post(f"/api/v1/projects/{project['id']}/publications")
    assert res.status_code == 200, res.text
    assert res.json()["version"]["version"] == 1
    assert res.json()["project"]["status"] == "published"

    versions = client.get(f"/api/v1/projects/{project['id']}/versions").json()
    assert [v["version"] for v in versions] == [1]

    restored = client.post(f"/api/v1/projects/{project['id']}/versions/1/restorations")
    assert restored.status_code == 200
    assert restored.json()["revision"] == 2


def test_empty_title_cannot_publish(client: TestClient, event, user) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()
    res = client.post(f"/api/v1/projects/{project['id']}/publications")
    assert res.status_code == 422


def test_gallery_only_lists_published(client: TestClient, event, user) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()
    assert client.get("/api/v1/projects").json() == []

    token = _lock(client, project["id"])
    client.patch(
        f"/api/v1/projects/{project['id']}/draft",
        json={"base_revision": 0, "title": "上架"},
        headers={"X-Edit-Token": token},
    )
    client.post(f"/api/v1/projects/{project['id']}/publications")

    gallery = client.get("/api/v1/projects").json()
    assert len(gallery) == 1
    assert gallery[0]["company_name"] == "Blue Fox"


def test_attendance_needs_editor(client: TestClient, event, user, other_user, mailer) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()

    res = client.put(
        f"/api/v1/projects/{project['id']}/attendance", json={"attendance": "yes"}
    )
    assert res.status_code == 200
    assert res.json()["attendance"] == "yes"

    company = client.get("/api/v1/companies/blue-fox").json()
    client.delete("/api/v1/auth/sessions/current")
    login(client, "ana@example.com", "ana-password-123")
    res = client.put(
        f"/api/v1/projects/{project['id']}/attendance", json={"attendance": "no"}
    )
    assert res.status_code == 403
    assert company["id"] == project["company_id"]


def test_lock_conflict_and_takeover(client: TestClient, event, user, other_user) -> None:
    _setup_company(client)
    project = client.get("/api/v1/projects/mine").json()
    _lock(client, project["id"])

    login(client, "ana@example.com", "ana-password-123")
    res = client.post(f"/api/v1/projects/{project['id']}/edit-sessions", json={})
    assert res.status_code == 403
