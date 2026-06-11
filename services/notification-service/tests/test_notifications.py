import asyncio
from datetime import UTC, datetime

from contracts.events import (
    PaymentApprovedEvent,
    PaymentFailedEvent,
    ReservationCreatedEvent,
    ReservationExpiredEvent,
    TicketIssuedEvent,
)
import pytest
from mongomock_motor import AsyncMongoMockClient
from fastapi.testclient import TestClient

from app.consumers import kafka_consumer
import app.database as database
from app.main import app
import app.main as app_main
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
OCCURRED_AT = datetime(2026, 5, 13, 10, tzinfo=UTC)


# ── 이벤트 픽스처 ──────────────────────────────────────────────

def reservation_created_event(user_id: str, source_id: str) -> dict:
    return ReservationCreatedEvent(
        eventId="event-reservation-1",
        userId=user_id,
        sourceId=source_id,
        reservationId=source_id,
        concertId="concert-1",
        seatId="seat-1",
        occurredAt=OCCURRED_AT,
        producer="reservation-service",
        correlationId="corr-1",
    ).model_dump(mode="json")


def reservation_expired_event(user_id: str, source_id: str) -> dict:
    return ReservationExpiredEvent(
        eventId="event-expired-1",
        userId=user_id,
        sourceId=source_id,
        reservationId=source_id,
        concertId="concert-1",
        seatId="seat-1",
        occurredAt=OCCURRED_AT,
        producer="reservation-service",
        correlationId="corr-2",
    ).model_dump(mode="json")


def payment_approved_event(user_id: str, source_id: str) -> dict:
    return PaymentApprovedEvent(
        eventId="event-payment-approved-1",
        userId=user_id,
        sourceId=source_id,
        paymentId=source_id,
        reservationId="reservation-1",
        concertId="concert-1",
        seatId="seat-1",
        amount=50000,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        correlationId="corr-3",
    ).model_dump(mode="json")


def payment_failed_event(user_id: str, source_id: str) -> dict:
    return PaymentFailedEvent(
        eventId="event-payment-failed-1",
        userId=user_id,
        sourceId=source_id,
        paymentId=source_id,
        reservationId="reservation-1",
        concertId="concert-1",
        seatId="seat-1",
        amount=50000,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        correlationId="corr-4",
    ).model_dump(mode="json")


def ticket_issued_event(user_id: str, source_id: str) -> dict:
    return TicketIssuedEvent(
        eventId="event-ticket-1",
        userId=user_id,
        sourceId=source_id,
        ticketId=source_id,
        reservationId="reservation-1",
        concertId="concert-1",
        seatId="seat-1",
        occurredAt=OCCURRED_AT,
        producer="ticket-service",
        correlationId="corr-5",
    ).model_dump(mode="json")


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
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notifications_created_total", event_type="reservation-created", result="success")
    assert_metric_labels(metrics, "notifications_created_total", event_type="reservation-created", result="duplicate")
    assert_metric_labels(metrics, "notification_events_consumed_total", event_type="reservation-created", result="duplicate", topic="reservation-created")


def test_invalid_business_event_records_failure_metric() -> None:
    import asyncio

    db = database.client["notification_db"]
    with pytest.raises(Exception):
        asyncio.get_event_loop().run_until_complete(handle_business_event(db, {"eventType": "payment-approved"}))

    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notification_events_consumed_total", event_type="payment-approved", result="failure", topic="payment-approved")


def test_consumer_skips_invalid_event_and_continues(monkeypatch: pytest.MonkeyPatch) -> None:
    import asyncio

    invalid_event = {
        "eventId": "event-invalid-user-id",
        "eventType": "reservation-created",
        "userId": 2,
        "sourceId": "reservation-invalid",
        "reservationId": "reservation-invalid",
        "concertId": "concert-1",
        "seatId": "seat-1",
        "occurredAt": OCCURRED_AT.isoformat().replace("+00:00", "Z"),
        "producer": "reservation-service",
        "correlationId": "corr-invalid",
    }
    valid_event = reservation_created_event(user_id="1", source_id="reservation-valid")
    fake_consumer = FakeConsumer(
        [
            FakeMessage(topic="reservation-created", value=invalid_event, offset=1),
            FakeMessage(topic="reservation-created", value=valid_event, offset=2),
        ]
    )

    def fake_consumer_factory(*args, **kwargs):
        assert kwargs["enable_auto_commit"] is False
        return fake_consumer

    monkeypatch.setattr(kafka_consumer.settings, "kafka_bootstrap_servers", "kafka:9092")
    asyncio.get_event_loop().run_until_complete(
        kafka_consumer.consume_events(asyncio.Event(), consumer_factory=fake_consumer_factory)
    )

    db = database.client["notification_db"]
    notifications = asyncio.get_event_loop().run_until_complete(db["notifications"].find().to_list(None))
    assert fake_consumer.commit_count == 2
    assert fake_consumer.stopped is True
    assert [doc["source_id"] for doc in notifications] == ["reservation-valid"]


def test_user_can_list_only_own_notifications() -> None:
    _seed_notifications()
    response = client.get("/notifications", headers=user_headers(1))

    assert response.status_code == 200
    assert all(item["userId"] == "1" for item in response.json())
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notification_reads_total", result="success", route_kind="list")


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
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notification_reads_total", result="rejection", route_kind="detail")


def test_healthz() -> None:
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


def test_lifespan_awaits_consumer_before_closing_db(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    async def fake_connect_db() -> None:
        calls.append("connect")

    async def fake_consume_events(stop_event) -> None:
        calls.append("consumer-start")
        await stop_event.wait()
        calls.append("consumer-stopped")

    def fake_close_db() -> None:
        calls.append("close")

    monkeypatch.setattr(app_main, "connect_db", fake_connect_db)
    monkeypatch.setattr(app_main, "consume_events", fake_consume_events)
    monkeypatch.setattr(app_main, "close_db", fake_close_db)

    with TestClient(app):
        assert app.state.consumer_task is not None

    assert app.state.consumer_task is None
    assert calls == ["connect", "consumer-start", "consumer-stopped", "close"]


def test_lifespan_cancels_consumer_after_shutdown_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    async def fake_connect_db() -> None:
        calls.append("connect")

    async def fake_consume_events(stop_event: asyncio.Event) -> None:
        calls.append("consumer-start")
        try:
            await asyncio.Event().wait()
        except asyncio.CancelledError:
            calls.append("consumer-cancelled")
            raise

    def fake_close_db() -> None:
        calls.append("close")

    monkeypatch.setattr(app_main, "_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS", 0.01)
    monkeypatch.setattr(app_main, "connect_db", fake_connect_db)
    monkeypatch.setattr(app_main, "consume_events", fake_consume_events)
    monkeypatch.setattr(app_main, "close_db", fake_close_db)

    with TestClient(app):
        assert app.state.consumer_task is not None

    assert app.state.consumer_task is None
    assert app.state.consumer_stop_event is None
    assert calls == ["connect", "consumer-start", "consumer-cancelled", "close"]


def test_readyz() -> None:
    response = client.get("/readyz")
    assert response.status_code == 200
    assert response.json()["status"] == "ok"


def test_metrics_returns_prometheus_format() -> None:
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_server_request_duration_seconds" in response.text
    assert "http_server_active_requests" in response.text
    assert "service_ready" in response.text
    assert 'service_name="notification-service"' in response.text
    assert 'http_request_method="GET"' in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_response_status_code="200"' in response.text


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


def assert_metric_labels(metrics: str, metric_name: str, **labels: str) -> None:
    label_fragments = [f'{key}="{value}"' for key, value in {"service_name": "notification-service", **labels}.items()]
    assert any(line.startswith(metric_name + "{") and all(fragment in line for fragment in label_fragments) for line in metrics.splitlines())


class FakeMessage:
    def __init__(self, *, topic: str, value: dict, offset: int) -> None:
        self.topic = topic
        self.value = value
        self.partition = 0
        self.offset = offset
        self.headers = []


class FakeConsumer:
    def __init__(self, messages: list[FakeMessage]) -> None:
        self.messages = messages
        self.commit_count = 0
        self.stopped = False

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        self.stopped = True

    async def commit(self) -> None:
        self.commit_count += 1

    def __aiter__(self):
        return self

    async def __anext__(self) -> FakeMessage:
        if not self.messages:
            raise StopAsyncIteration
        return self.messages.pop(0)
