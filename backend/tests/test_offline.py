"""线下场次：报名、候补、签到与日历导出。"""

from datetime import UTC, datetime, timedelta

from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import hash_password
from app.models.identity import User
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


def _create_session(client: TestClient, seats: int = 1) -> dict:
    starts_at = (datetime.now(UTC) + timedelta(days=2)).isoformat()
    res = client.post(
        "/api/v1/admin/offline-sessions",
        json={
            "title": "线下见面会",
            "location": "上海",
            "starts_at": starts_at,
            "seats": seats,
        },
    )
    assert res.status_code == 201, res.text
    return res.json()


def test_waitlist_and_promotion(client: TestClient, db_session, event, user, other_user) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    session = _create_session(client, seats=1)
    assert session["seats_left"] == 1

    login(client, "will@example.com", "correct-horse-battery")
    res = client.put(f"/api/v1/offline-sessions/{session['id']}/rsvp", json={"answer": "going"})
    assert res.status_code == 200
    assert res.json()["waitlisted"] is False

    login(client, "ana@example.com", "ana-password-123")
    res = client.put(f"/api/v1/offline-sessions/{session['id']}/rsvp", json={"answer": "going"})
    assert res.status_code == 200
    assert res.json()["waitlisted"] is True

    login(client, "staff@example.com", "staff-password-123")
    res = client.patch(
        f"/api/v1/admin/offline-sessions/{session['id']}", json={"seats": 2}
    )
    assert res.status_code == 200
    assert res.json()["going_count"] == 2

    login(client, "ana@example.com", "ana-password-123")
    detail = client.get(f"/api/v1/offline-sessions/{session['id']}").json()
    assert detail["mine"]["waitlisted"] is False


def test_cancel_rsvp_promotes_next(
    client: TestClient, db_session, event, user, other_user
) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    session = _create_session(client, seats=1)

    login(client, "will@example.com", "correct-horse-battery")
    client.put(f"/api/v1/offline-sessions/{session['id']}/rsvp", json={"answer": "going"})
    login(client, "ana@example.com", "ana-password-123")
    client.put(f"/api/v1/offline-sessions/{session['id']}/rsvp", json={"answer": "going"})

    login(client, "will@example.com", "correct-horse-battery")
    assert client.delete(f"/api/v1/offline-sessions/{session['id']}/rsvp").status_code == 200

    detail = client.get(f"/api/v1/offline-sessions/{session['id']}").json()
    assert detail["going_count"] == 1

    login(client, "ana@example.com", "ana-password-123")
    detail = client.get(f"/api/v1/offline-sessions/{session['id']}").json()
    assert detail["mine"]["waitlisted"] is False


def test_calendar_export(client: TestClient, db_session, event) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    session = _create_session(client)

    res = client.get(f"/api/v1/offline-sessions/{session['id']}/calendar.ics")
    assert res.status_code == 200
    assert res.headers["content-type"].startswith("text/calendar")
    assert "BEGIN:VCALENDAR" in res.text
    assert "线下见面会" in res.text


def test_check_in_and_cancel(client: TestClient, db_session, event, user) -> None:
    _staff(db_session)
    login(client, "staff@example.com", "staff-password-123")
    session = _create_session(client)

    login(client, "will@example.com", "correct-horse-battery")
    client.put(f"/api/v1/offline-sessions/{session['id']}/rsvp", json={"answer": "going"})

    login(client, "staff@example.com", "staff-password-123")
    rsvps = client.get(f"/api/v1/admin/offline-sessions/{session['id']}/rsvps").json()
    assert len(rsvps) == 1
    res = client.put(
        f"/api/v1/admin/offline-sessions/{session['id']}/rsvps/{rsvps[0]['id']}/check-in",
        json={"checked_in": True},
    )
    assert res.status_code == 200

    res = client.post(f"/api/v1/admin/offline-sessions/{session['id']}/cancellations")
    assert res.status_code == 200
    assert client.get(f"/api/v1/offline-sessions/{session['id']}").json()["status"] == "cancelled"
