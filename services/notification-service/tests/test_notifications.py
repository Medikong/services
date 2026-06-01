import pytest
from mongomock_motor import AsyncMongoMockClient
from fastapi.testclient import TestClient

import app.database as database
from app.main import app
from app.services.notification_service import handle_business_event


def override_get_db():
    return database.client["notification_db"]


app.dependency_overrides[database.get_db] = override_get_db


@pytest.fixture(autouse=True)
def setup_mock_db():
    database.client = AsyncMongoMockClient()
    yield
    database.client = None


client = TestClient(app, raise_server_exceptions=True)


# ── 이벤트 픽스처 ──────────────────────────────────────────────

def reservation_created_event(user_id: str, source_id: str) -> dict:
    return {
        "eventId": "event-reservation-1",
        "eventType": "reservation-created",
        "userId": user_id,
        "sourceId": source_id,
        "concertId": "concert-1",
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "reservation-service",
        "correlationId": "corr-1",
    }


def reservation_expired_event(user_id: str, source_id: str) -> dict:
    return {
        "eventId": "event-expired-1",
        "eventType": "reservation-expired",
        "userId": user_id,
        "sourceId": source_id,
        "occurredAt": "2026-05-13T10:05:00Z",
        "producer": "reservation-service",
        "correlationId": "corr-2",
    }


def payment_approved_event(user_id: str, source_id: str) -> dict:
    return {
        "eventId": "event-payment-approved-1",
        "eventType": "payment-approved",
        "userId": user_id,
        "sourceId": source_id,
        "reservationId": "reservation-1",
        "occurredAt": "2026-05-13T10:10:00Z",
        "producer": "payment-service",
        "correlationId": "corr-3",
    }


def payment_failed_event(user_id: str, source_id: str) -> dict:
    return {
        "eventId": "event-payment-failed-1",
        "eventType": "payment-failed",
        "userId": user_id,
        "sourceId": source_id,
        "reservationId": "reservation-1",
        "occurredAt": "2026-05-13T10:10:00Z",
        "producer": "payment-service",
        "correlationId": "corr-4",
    }


def ticket_issued_event(user_id: str, source_id: str) -> dict:
    return {
        "eventId": "event-ticket-1",
        "eventType": "ticket-issued",
        "userId": user_id,
        "sourceId": source_id,
        "reservationId": "reservation-1",
        "occurredAt": "2026-05-13T10:15:00Z",
        "producer": "ticket-service",
        "correlationId": "corr-5",
    }


def user_headers(user_id: int | str) -> dict[str, str]:
    return {"X-User-Id": str(user_id), "X-User-Role": "USER"}


# ── 단위 테스트 ────────────────────────────────────────────────

def test_reservation_created_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id="reservation-1"))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "reservation-created"
    assert "예매가 생성되었습니다" in notification["message"]


def test_reservation_expired_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, reservation_expired_event(user_id="1", source_id="reservation-1"))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "reservation-expired"
    assert "만료" in notification["message"]


def test_payment_approved_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, payment_approved_event(user_id="1", source_id="payment-1"))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "payment-approved"
    assert "결제가 완료되었습니다" in notification["message"]


def test_payment_failed_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, payment_failed_event(user_id="1", source_id="payment-1"))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "payment-failed"
    assert "실패" in notification["message"]


def test_ticket_issued_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, ticket_issued_event(user_id="1", source_id="ticket-1"))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "ticket-issued"
    assert "티켓이 발행되었습니다" in notification["message"]


def test_duplicate_event_id_returns_existing_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    first = loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id="reservation-1"))
    )
    second = loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id="reservation-1"))
    )
    count = loop.run_until_complete(db["notifications"].count_documents({}))

    assert second["id"] == first["id"]
    assert count == 1  # 중복 처리 없음


def test_user_can_list_only_own_notifications() -> None:
    _seed_notifications()
    response = client.get("/notifications", headers=user_headers(1))

    assert response.status_code == 200
    assert all(item["userId"] == "1" for item in response.json())


def test_user_cannot_read_other_user_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="2", source_id="reservation-2"))
    )
    notifications = loop.run_until_complete(db["notifications"].find().to_list(None))
    other_id = str(notifications[0]["_id"])

    response = client.get(f"/notifications/{other_id}", headers=user_headers(1))
    assert response.status_code == 403


def test_healthz() -> None:
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


def test_readyz() -> None:
    response = client.get("/readyz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


# ── 헬퍼 ──────────────────────────────────────────────────────

def _seed_notifications() -> None:
    import asyncio
    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id="reservation-1"))
    )
    loop.run_until_complete(
        handle_business_event(db, payment_approved_event(user_id="2", source_id="payment-2"))
    )
