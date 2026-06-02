from collections.abc import Generator

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker
from sqlalchemy.pool import StaticPool

from app.database import Base, get_db
from app.main import app
from app.services import ticket_service


engine = create_engine(
    "sqlite://",
    connect_args={"check_same_thread": False},
    poolclass=StaticPool,
)
TestingSessionLocal = sessionmaker(autocommit=False, autoflush=False, bind=engine)


def override_get_db() -> Generator[Session, None, None]:
    db = TestingSessionLocal()
    try:
        yield db
    finally:
        db.close()


app.dependency_overrides[get_db] = override_get_db
client = TestClient(app)


@pytest.fixture(autouse=True)
def reset_db() -> Generator[None, None, None]:
    Base.metadata.drop_all(bind=engine)
    Base.metadata.create_all(bind=engine)
    yield


# ── 단위 테스트 ────────────────────────────────────────────────

def test_issue_ticket_creates_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    response = client.post("/tickets/issue", json=ticket_issue_request())

    assert response.status_code == 200
    assert response.json()["reservationId"] == "reservation-1"
    assert response.json()["status"] == "ISSUED"


def test_duplicate_issue_returns_existing_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    first = client.post("/tickets/issue", json=ticket_issue_request())
    second = client.post("/tickets/issue", json=ticket_issue_request())

    assert first.json()["id"] == second.json()["id"]


def test_issue_ticket_publishes_ticket_issued_event(monkeypatch: pytest.MonkeyPatch) -> None:
    published: list[tuple[str, dict]] = []

    async def fake_publish(topic: str, payload: dict) -> bool:
        published.append((topic, payload))
        return True

    monkeypatch.setattr(ticket_service.kafka, "publish_event", fake_publish)
    monkeypatch.setattr(ticket_service.s3, "upload_qr", lambda *args: None)
    monkeypatch.setattr(ticket_service.s3, "upload_pdf", lambda *args: None)

    client.post("/tickets/issue", json=ticket_issue_request())

    assert published[0][0] == "ticket-issued"
    assert published[0][1]["eventType"] == "ticket-issued"
    assert published[0][1]["ticketId"] == str(published[0][1]["sourceId"])
    assert published[0][1]["concertId"] == "concert-1"
    assert published[0][1]["seatId"] == "seat-A1"


def test_user_can_get_own_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = client.post("/tickets/issue", json=ticket_issue_request()).json()
    response = client.get(f"/tickets/{issued['id']}", headers=user_headers(1))

    assert response.status_code == 200
    assert response.json()["id"] == issued["id"]


def test_user_can_list_my_tickets(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = client.post("/tickets/issue", json=ticket_issue_request()).json()
    response = client.get("/tickets/me", headers=user_headers(1))

    assert response.status_code == 200
    assert response.json()[0]["id"] == issued["id"]


def test_user_cannot_get_other_user_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = client.post("/tickets/issue", json=ticket_issue_request()).json()
    response = client.get(f"/tickets/{issued['id']}", headers=user_headers(99))

    assert response.status_code == 403


def test_payment_approved_event_issues_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    import asyncio
    _mock_kafka_and_s3(monkeypatch)

    db = TestingSessionLocal()
    loop = asyncio.get_event_loop()
    loop.run_until_complete(
        ticket_service.handle_payment_approved(db, payment_approved_event())
    )

    from app.models import Ticket
    ticket = db.query(Ticket).first()
    assert ticket is not None
    assert ticket.reservation_id == "reservation-1"


def test_duplicate_payment_event_does_not_create_duplicate_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    import asyncio
    _mock_kafka_and_s3(monkeypatch)

    db = TestingSessionLocal()
    loop = asyncio.get_event_loop()
    loop.run_until_complete(ticket_service.handle_payment_approved(db, payment_approved_event()))
    loop.run_until_complete(ticket_service.handle_payment_approved(db, payment_approved_event()))

    from app.models import Ticket
    count = db.query(Ticket).count()
    assert count == 1


def test_healthz() -> None:
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


def test_readyz() -> None:
    response = client.get("/readyz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


def test_metrics_returns_prometheus_format() -> None:
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_requests_total" in response.text
    assert 'service="ticket-service"' in response.text
    assert 'method="GET"' in response.text
    assert 'path="/healthz"' in response.text
    assert 'status="200"' in response.text


# ── 헬퍼 ──────────────────────────────────────────────────────

def ticket_issue_request() -> dict:
    return {
        "reservationId": "reservation-1",
        "userId": "1",
        "concertId": "concert-1",
        "seatId": "seat-A1",
    }


def payment_approved_event() -> dict:
    return {
        "eventId": "event-payment-1",
        "eventType": "payment-approved",
        "userId": "1",
        "sourceId": "payment-1",
        "reservationId": "reservation-1",
        "concertId": "concert-1",
        "seatId": "seat-A1",
        "paymentId": "payment-1",
        "amount": 50000,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "payment-service",
        "correlationId": "corr-1",
    }


def user_headers(user_id: int | str) -> dict[str, str]:
    return {"X-User-Id": str(user_id), "X-User-Role": "USER"}


def _mock_kafka_and_s3(monkeypatch: pytest.MonkeyPatch) -> None:
    async def fake_publish(topic: str, payload: dict) -> bool:
        return True

    monkeypatch.setattr(ticket_service.kafka, "publish_event", fake_publish)
    monkeypatch.setattr(ticket_service.s3, "upload_qr", lambda *args: None)
    monkeypatch.setattr(ticket_service.s3, "upload_pdf", lambda *args: None)
