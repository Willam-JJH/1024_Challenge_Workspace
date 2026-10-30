"""测试夹具：内存 SQLite + 覆写数据库依赖。"""

from collections.abc import Iterator

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker
from sqlalchemy.pool import StaticPool

import app.models  # noqa: F401 - 注册全部模型
from app.core.db import Base, get_db
from app.core.security import hash_password
from app.main import app as fastapi_app
from app.models.event import Event
from app.models.identity import User


@pytest.fixture()
def db_session() -> Iterator[Session]:
    engine = create_engine(
        "sqlite://",
        connect_args={"check_same_thread": False},
        poolclass=StaticPool,
    )
    Base.metadata.create_all(engine)
    TestingSession = sessionmaker(bind=engine, autoflush=False, expire_on_commit=False)
    session = TestingSession()
    try:
        yield session
    finally:
        session.close()
        engine.dispose()


@pytest.fixture()
def client(db_session: Session) -> Iterator[TestClient]:
    def _override() -> Iterator[Session]:
        yield db_session
        db_session.commit()

    fastapi_app.dependency_overrides[get_db] = _override
    with TestClient(fastapi_app) as c:
        yield c
    fastapi_app.dependency_overrides.clear()


@pytest.fixture()
def event(db_session: Session) -> Event:
    row = Event(slug="1024-challenge", name="1024 Challenge", stage="registration")
    db_session.add(row)
    db_session.commit()
    return row


@pytest.fixture()
def user(db_session: Session) -> User:
    row = User(
        email="will@example.com",
        password_hash=hash_password("correct-horse-battery"),
        display_name="Will",
    )
    db_session.add(row)
    db_session.commit()
    return row


class CapturingMailer:
    """测试用邮件驱动：把验证码留在内存里。"""

    def __init__(self) -> None:
        self.codes: list[tuple[str, str, str]] = []
        self.notices: list[tuple[str, str, str]] = []

    def send_code(self, *, to: str, code: str, purpose: str, locale: str = "zh") -> None:
        self.codes.append((to, code, purpose))

    def send_notice(self, *, to: str, subject: str, body: str) -> None:
        self.notices.append((to, subject, body))

    def last_code(self, purpose: str) -> str:
        for _to, code, kind in reversed(self.codes):
            if kind == purpose:
                return code
        raise AssertionError(f"没有 {purpose} 验证码")


@pytest.fixture()
def mailer() -> Iterator[CapturingMailer]:
    from app.services.mailer import ConsoleMailer, set_mailer

    box = CapturingMailer()
    set_mailer(box)
    try:
        yield box
    finally:
        set_mailer(ConsoleMailer())


def login(client: TestClient, email: str, password: str) -> None:
    res = client.post("/api/v1/auth/sessions", json={"email": email, "password": password})
    assert res.status_code == 200, res.text


@pytest.fixture()
def other_user(db_session: Session) -> User:
    row = User(
        email="ana@example.com",
        password_hash=hash_password("ana-password-123"),
        display_name="Ana",
    )
    db_session.add(row)
    db_session.commit()
    return row
