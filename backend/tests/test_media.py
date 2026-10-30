"""素材：上传、分片、读取与下架。"""

from pathlib import Path

from fastapi.testclient import TestClient

from app.core.config import settings
from tests.conftest import login

PNG = b"\x89PNG\r\n\x1a\n" + b"\x00" * 64


def _prepare(monkeypatch, tmp_path: Path) -> None:
    monkeypatch.setattr(settings, "upload_dir", str(tmp_path))


def test_upload_and_read(client: TestClient, event, user, monkeypatch, tmp_path) -> None:
    _prepare(monkeypatch, tmp_path)
    login(client, "will@example.com", "correct-horse-battery")

    created = client.post(
        "/api/v1/media/assets",
        json={"filename": "logo.png", "content_type": "image/png", "purpose": "submission"},
    ).json()
    asset_id = created["asset"]["id"]
    assert created["upload_url"].endswith(f"/{asset_id}/content")

    res = client.put(
        f"/api/v1/media/assets/{asset_id}/content", content=PNG,
        headers={"Content-Type": "application/octet-stream"},
    )
    assert res.status_code == 200, res.text
    assert res.json()["status"] == "ready"
    assert res.json()["content_type"] == "image/png"

    got = client.get(f"/api/v1/files/{asset_id}")
    assert got.status_code == 200
    assert got.content == PNG

    variant = client.get(f"/api/v1/files/{asset_id}/thumb")
    assert variant.status_code == 404
    assert variant.json()["error"]["code"] == "variant_not_available"


def test_upload_requires_owner(
    client: TestClient, event, user, other_user, monkeypatch, tmp_path
) -> None:
    _prepare(monkeypatch, tmp_path)
    login(client, "will@example.com", "correct-horse-battery")
    asset_id = client.post(
        "/api/v1/media/assets", json={"filename": "a.txt"}
    ).json()["asset"]["id"]

    login(client, "ana@example.com", "ana-password-123")
    res = client.put(f"/api/v1/media/assets/{asset_id}/content", content=b"hi")
    assert res.status_code == 403


def test_parts_and_admin_removal(
    client: TestClient, db_session, event, user, monkeypatch, tmp_path
) -> None:
    _prepare(monkeypatch, tmp_path)
    login(client, "will@example.com", "correct-horse-battery")
    asset_id = client.post(
        "/api/v1/media/assets", json={"filename": "big.bin", "purpose": "pitch"}
    ).json()["asset"]["id"]

    assert client.put(f"/api/v1/media/assets/{asset_id}/parts/1", content=b"aa").status_code == 200
    assert client.put(f"/api/v1/media/assets/{asset_id}/parts/2", content=b"bb").status_code == 200
    res = client.post(f"/api/v1/media/assets/{asset_id}/completions")
    assert res.json()["size"] == 4

    from subprocess import run  # noqa: F401  (保持导入位置稳定)

    from sqlalchemy import select

    from app.core.security import hash_password
    from app.models.identity import User

    staff = User(
        email="staff@example.com",
        password_hash=hash_password("staff-password-123"),
        display_name="Staff",
        role="staff",
    )
    db_session.add(staff)
    db_session.commit()

    login(client, "staff@example.com", "staff-password-123")
    assert client.delete(f"/api/v1/admin/media/{asset_id}").status_code == 200
    assert client.get(f"/api/v1/media/assets/{asset_id}").status_code == 404
    assert select(User) is not None
