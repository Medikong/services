import asyncio
from datetime import UTC, datetime

from bson import ObjectId
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
from server.ids import deterministic_uuid_string

from app.consumers import kafka_consumer
import app.database as database
from app.main import app
import app.main as app_main
from app.services.notification_service import handle_business_event
import app.worker as worker_module


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
RESERVATION_ID = deterministic_uuid_string("notification-service-test", "reservation", 1)
RESERVATION_2_ID = deterministic_uuid_string("notification-service-test", "reservation", 2)
RESERVATION_VALID_ID = deterministic_uuid_string("notification-service-test", "reservation", "valid")
RESERVATION_INVALID_ID = deterministic_uuid_string("notification-service-test", "reservation", "invalid")
PAYMENT_ID = deterministic_uuid_string("notification-service-test", "payment", 1)
PAYMENT_2_ID = deterministic_uuid_string("notification-service-test", "payment", 2)
TICKET_ID = deterministic_uuid_string("notification-service-test", "ticket", 1)
CONCERT_ID = deterministic_uuid_string("notification-service-test", "concert", 1)
SEAT_ID = deterministic_uuid_string("notification-service-test", "seat", 1)


# ── 이벤트 픽스처 ──────────────────────────────────────────────

def reservation_created_event(user_id: str, source_id: str) -> dict:
    return ReservationCreatedEvent(
        eventId=deterministic_uuid_string("notification-service-test", "event", "reservation-created", 1),
        userId=user_id,
        sourceId=source_id,
        reservationId=source_id,
        concertId=CONCERT_ID,
        seatId=SEAT_ID,
        occurredAt=OCCURRED_AT,
        producer="reservation-service",
        correlationId="corr-1",
    ).model_dump(mode="json")


def reservation_expired_event(user_id: str, source_id: str) -> dict:
    return ReservationExpiredEvent(
        eventId=deterministic_uuid_string("notification-service-test", "event", "reservation-expired", 1),
        userId=user_id,
        sourceId=source_id,
        reservationId=source_id,
        concertId=CONCERT_ID,
        seatId=SEAT_ID,
        occurredAt=OCCURRED_AT,
        producer="reservation-service",
        correlationId="corr-2",
    ).model_dump(mode="json")


def payment_approved_event(user_id: str, source_id: str) -> dict:
    return PaymentApprovedEvent(
        eventId=deterministic_uuid_string("notification-service-test", "event", "payment-approved", 1),
        userId=user_id,
        sourceId=source_id,
        paymentId=source_id,
        reservationId=RESERVATION_ID,
        concertId=CONCERT_ID,
        seatId=SEAT_ID,
        amount=50000,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        correlationId="corr-3",
    ).model_dump(mode="json")


def payment_failed_event(user_id: str, source_id: str) -> dict:
    return PaymentFailedEvent(
        eventId=deterministic_uuid_string("notification-service-test", "event", "payment-failed", 1),
        userId=user_id,
        sourceId=source_id,
        paymentId=source_id,
        reservationId=RESERVATION_ID,
        concertId=CONCERT_ID,
        seatId=SEAT_ID,
        amount=50000,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        correlationId="corr-4",
    ).model_dump(mode="json")


def ticket_issued_event(user_id: str, source_id: str) -> dict:
    return TicketIssuedEvent(
        eventId=deterministic_uuid_string("notification-service-test", "event", "ticket-issued", 1),
        userId=user_id,
        sourceId=source_id,
        ticketId=source_id,
        reservationId=RESERVATION_ID,
        concertId=CONCERT_ID,
        seatId=SEAT_ID,
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
        handle_business_event(db, reservation_created_event(user_id="1", source_id=RESERVATION_ID))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "reservation-created"
    assert "예매가 생성되었습니다" in notification["message"]


def test_reservation_expired_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, reservation_expired_event(user_id="1", source_id=RESERVATION_ID))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "reservation-expired"
    assert "만료" in notification["message"]


def test_payment_approved_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, payment_approved_event(user_id="1", source_id=PAYMENT_ID))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "payment-approved"
    assert "결제가 완료되었습니다" in notification["message"]


def test_payment_failed_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, payment_failed_event(user_id="1", source_id=PAYMENT_ID))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "payment-failed"
    assert "실패" in notification["message"]


def test_ticket_issued_event_creates_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    notification = asyncio.get_event_loop().run_until_complete(
        handle_business_event(db, ticket_issued_event(user_id="1", source_id=TICKET_ID))
    )

    assert notification["userId"] == "1"
    assert notification["type"] == "ticket-issued"
    assert "티켓이 발행되었습니다" in notification["message"]


def test_duplicate_event_id_returns_existing_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    first = loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id=RESERVATION_ID))
    )
    second = loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="1", source_id=RESERVATION_ID))
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
        "eventId": deterministic_uuid_string("notification-service-test", "event", "invalid-user-id"),
        "eventType": "reservation-created",
        "userId": 2,
        "sourceId": RESERVATION_INVALID_ID,
        "reservationId": RESERVATION_INVALID_ID,
        "concertId": CONCERT_ID,
        "seatId": SEAT_ID,
        "occurredAt": OCCURRED_AT.isoformat().replace("+00:00", "Z"),
        "producer": "reservation-service",
        "correlationId": "corr-invalid",
    }
    valid_event = reservation_created_event(user_id="1", source_id=RESERVATION_VALID_ID)
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
    assert [doc["source_id"] for doc in notifications] == [RESERVATION_VALID_ID]


def test_user_can_list_only_own_notifications() -> None:
    _seed_notifications()
    response = client.get("/notifications", headers=user_headers(1))

    assert response.status_code == 200
    body = response.json()
    assert all(item["userId"] == "1" for item in body["items"])
    assert body["page"] == {"nextCursor": None, "hasMore": False, "limit": 20}
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notification_reads_total", result="success", route_kind="list")


def test_list_notifications_applies_limit_and_returns_next_cursor() -> None:
    inserted_ids = _insert_notifications_for_user("1", 3)

    response = client.get("/notifications?limit=2", headers=user_headers(1))

    assert response.status_code == 200
    body = response.json()
    assert [item["id"] for item in body["items"]] == [inserted_ids[2], inserted_ids[1]]
    assert body["page"] == {"nextCursor": inserted_ids[1], "hasMore": True, "limit": 2}


def test_list_notifications_uses_cursor_for_next_page() -> None:
    inserted_ids = _insert_notifications_for_user("1", 3)
    first_page = client.get("/notifications?limit=2", headers=user_headers(1)).json()

    response = client.get(
        f"/notifications?limit=2&cursor={first_page['page']['nextCursor']}",
        headers=user_headers(1),
    )

    assert response.status_code == 200
    body = response.json()
    assert [item["id"] for item in body["items"]] == [inserted_ids[0]]
    assert body["page"] == {"nextCursor": None, "hasMore": False, "limit": 2}


def test_list_notifications_does_not_mix_other_user_notifications() -> None:
    user_one_ids = _insert_notifications_for_user("1", 2)
    _insert_notifications_for_user("99", 3)

    response = client.get("/notifications?limit=10", headers=user_headers(1))

    assert response.status_code == 200
    body = response.json()
    assert [item["id"] for item in body["items"]] == [user_one_ids[1], user_one_ids[0]]
    assert {item["userId"] for item in body["items"]} == {"1"}
    assert body["page"]["hasMore"] is False


def test_list_notifications_rejects_invalid_cursor() -> None:
    response = client.get("/notifications?cursor=not-an-object-id", headers=user_headers(1))

    assert response.status_code == 400
    assert response.json()["detail"] == "Invalid notification cursor"
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "notification_reads_total", result="rejection", route_kind="list")


def test_ensure_indexes_creates_notification_and_processed_event_indexes() -> None:
    import asyncio

    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    loop.run_until_complete(database.ensure_indexes())

    notification_indexes = {
        index["name"]: index
        for index in loop.run_until_complete(db["notifications"].list_indexes().to_list(None))
    }
    processed_event_indexes = {
        index["name"]: index
        for index in loop.run_until_complete(db["processed_events"].list_indexes().to_list(None))
    }

    assert notification_indexes["user_id_1__id_-1"]["key"] == {"user_id": 1, "_id": -1}
    assert processed_event_indexes["event_id_1"]["key"] == {"event_id": 1}
    assert processed_event_indexes["event_id_1"]["unique"] is True


def test_user_cannot_read_other_user_notification() -> None:
    import asyncio
    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    loop.run_until_complete(
        handle_business_event(db, reservation_created_event(user_id="2", source_id=RESERVATION_2_ID))
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


def test_lifespan_connects_and_closes_db_without_consumer(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    async def fake_connect_db() -> None:
        calls.append("connect")

    def fake_close_db() -> None:
        calls.append("close")

    monkeypatch.setattr(app_main, "connect_db", fake_connect_db)
    monkeypatch.setattr(app_main, "close_db", fake_close_db)

    with TestClient(app):
        pass

    assert calls == ["connect", "close"]


def test_worker_awaits_consumer_before_closing_db(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    async def fake_connect_db() -> None:
        calls.append("connect")

    async def fake_consume_events(stop_event) -> None:
        calls.append("consumer-start")
        stop_event.set()

    def fake_close_db() -> None:
        calls.append("close")

    monkeypatch.setattr(worker_module, "_install_signal_handlers", lambda stop_event: None)
    monkeypatch.setattr(worker_module, "connect_db", fake_connect_db)
    monkeypatch.setattr(worker_module, "consume_events", fake_consume_events)
    monkeypatch.setattr(worker_module, "close_db", fake_close_db)

    asyncio.run(worker_module.run_worker())

    assert calls == ["connect", "consumer-start", "close"]


def test_worker_cancels_consumer_after_shutdown_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
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

    monkeypatch.setattr(worker_module, "_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS", 0.01)
    monkeypatch.setattr(worker_module, "_install_signal_handlers", lambda stop_event: stop_event.set())
    monkeypatch.setattr(worker_module, "connect_db", fake_connect_db)
    monkeypatch.setattr(worker_module, "consume_events", fake_consume_events)
    monkeypatch.setattr(worker_module, "close_db", fake_close_db)

    asyncio.run(worker_module.run_worker())

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
        handle_business_event(db, reservation_created_event(user_id="1", source_id=RESERVATION_ID))
    )
    loop.run_until_complete(
        handle_business_event(db, payment_approved_event(user_id="2", source_id=PAYMENT_2_ID))
    )


def _insert_notifications_for_user(user_id: str, count: int) -> list[str]:
    import asyncio

    db = database.client["notification_db"]
    loop = asyncio.get_event_loop()
    inserted_ids: list[str] = []
    for index in range(count):
        notification_id = ObjectId()
        loop.run_until_complete(
            db["notifications"].insert_one(
                {
                    "_id": notification_id,
                    "user_id": user_id,
                    "type": "reservation-created",
                    "message": f"notification {index}",
                    "status": "CREATED",
                    "source_id": deterministic_uuid_string("notification-service-test", "notification-source", user_id, index),
                    "metadata": {},
                    "created_at": OCCURRED_AT,
                }
            )
        )
        inserted_ids.append(str(notification_id))
    return inserted_ids


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
