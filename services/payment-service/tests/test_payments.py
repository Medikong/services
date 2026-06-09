import asyncio
import os
from pathlib import Path

import pytest

Path("test_payment_service.db").unlink(missing_ok=True)
os.environ["DATABASE_URL"] = "sqlite:///./test_payment_service.db"
os.environ["JWT_SECRET"] = "ticketing-dev-secret"
os.environ["SERVICE_VERSION"] = "test-version"
os.environ["SERVICE_ENVIRONMENT"] = "test"

from fastapi.testclient import TestClient  # noqa: E402

from app.database import Base, SessionLocal, engine  # noqa: E402
from app.main import app  # noqa: E402
from app.metrics.recorder import PaymentTelemetryRecorder  # noqa: E402
from app.models import PaymentEvent  # noqa: E402
from app.services.payment_events import PaymentEventDispatcher, run_payment_event_dispatcher  # noqa: E402


client = TestClient(app)


@pytest.fixture(autouse=True)
def reset_db() -> None:
    Base.metadata.drop_all(bind=engine)
    Base.metadata.create_all(bind=engine)
    app.dependency_overrides.clear()


def test_create_and_get_approved_payment() -> None:
    producer = FakeKafkaProducer()
    headers = auth_headers("CUSTOMER", user_id="1")
    response = client.post(
        "/payments",
        headers=headers,
        json={
            "reservationId": "res-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    payment = response.json()
    assert payment["reservationId"] == "res-1"
    assert payment["concertId"] == "concert-1"
    assert payment["status"] == "approved"
    assert payment["approvedAt"] is not None
    events = payment_events()
    assert [event.event_type for event in events] == ["payment-approved"]
    assert events[0].publish_status == "pending"
    assert events[0].publish_attempts == 0
    assert events[0].published_at is None
    assert producer.sent == []

    get_response = client.get(f"/payments/{payment['id']}", headers=headers)
    assert get_response.status_code == 200
    assert get_response.json()["id"] == payment["id"]


def test_payment_simulation_fail_and_delay() -> None:
    producer = FakeKafkaProducer()
    fail_response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="2"),
        json={
            "reservationId": "res-2",
            "concertId": "concert-1",
            "amount": 50000,
            "method": "mock",
            "simulation": "fail",
        },
    )
    delay_response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="3"),
        json={
            "reservationId": "res-3",
            "concertId": "concert-1",
            "amount": 50000,
            "method": "mock",
            "simulation": "delay",
        },
    )

    assert fail_response.status_code == 201
    assert fail_response.json()["status"] == "failed"
    assert delay_response.status_code == 201
    assert delay_response.json()["status"] == "delayed"
    events = payment_events()
    assert [event.event_type for event in events] == ["payment-failed"]
    assert events[0].publish_status == "pending"
    assert producer.sent == []


def test_idempotency_key_returns_existing_payment() -> None:
    headers = auth_headers("CUSTOMER", user_id="4") | {"Idempotency-Key": "idem-1"}
    payload = {
        "reservationId": "res-4",
        "concertId": "concert-2",
        "amount": 30000,
        "method": "mock",
        "simulation": "approve",
    }

    first = client.post("/payments", headers=headers, json=payload)
    second = client.post("/payments", headers=headers, json=payload)

    assert first.status_code == 201
    assert second.status_code == 201
    assert second.json()["id"] == first.json()["id"]
    assert len(payment_events()) == 1


def test_customer_cannot_read_other_customer_payment() -> None:
    created = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="5"),
        json={
            "reservationId": "res-5",
            "concertId": "concert-3",
            "amount": 40000,
            "method": "mock",
        },
    )

    response = client.get(f"/payments/{created.json()['id']}", headers=auth_headers("CUSTOMER", user_id="6"))

    assert response.status_code == 403
    assert response.json()["error"]["code"] == "auth.forbidden"


def test_provider_and_admin_can_get_settlement_basis() -> None:
    client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="7"),
        json={
            "reservationId": "res-7",
            "concertId": "concert-4",
            "amount": 100000,
            "method": "mock",
        },
    )

    provider_response = client.get(
        "/provider/concerts/concert-4/settlement-basis",
        headers=auth_headers("PROVIDER", user_id="provider-1"),
    )
    admin_response = client.get(
        "/admin/concerts/concert-4/settlement-basis",
        headers=auth_headers("ADMIN", user_id="admin-1"),
    )

    assert provider_response.status_code == 200
    assert provider_response.json()["grossAmount"] == 100000
    assert provider_response.json()["platformFeeAmount"] == 10000
    assert admin_response.status_code == 200


def test_operational_endpoints_and_error_shape() -> None:
    assert client.get("/healthz").status_code == 200
    assert client.get("/readyz").json()["checks"]["database"] == "ok"
    metrics_response = client.get("/metrics")
    assert metrics_response.status_code == 200
    assert metrics_response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_server_request_duration_seconds" in metrics_response.text
    assert "http_server_active_requests" in metrics_response.text
    assert "service_ready" in metrics_response.text
    assert 'service_name="payment-service"' in metrics_response.text
    assert 'http_request_method="GET"' in metrics_response.text
    assert 'http_route="/healthz"' in metrics_response.text
    assert 'http_response_status_code="200"' in metrics_response.text

    response = client.get("/payments/pay-missing")
    assert response.status_code == 401
    assert response.json()["error"]["code"] == "auth.invalid_token"


def test_payment_metrics_record_results_duration_and_event_publish_success() -> None:
    producer = FakeKafkaProducer()

    for user_id, simulation in (("11", "approve"), ("12", "fail"), ("13", "delay")):
        response = client.post(
            "/payments",
            headers=auth_headers("CUSTOMER", user_id=user_id),
            json={
                "reservationId": f"res-{user_id}",
                "concertId": "concert-metrics",
                "amount": 50000,
                "method": "mock",
                "simulation": simulation,
            },
        )
        assert response.status_code == 201

    assert producer.sent == []
    assert asyncio.run(dispatch_pending_events(producer)) == 2
    metrics_response = client.get("/metrics")
    metrics_text = metrics_response.text

    assert "payments_total" in metrics_text
    assert 'method="mock"' in metrics_text
    assert 'result="success"' in metrics_text
    assert 'result="failure"' in metrics_text
    assert 'result="delayed"' in metrics_text
    assert 'error_code="none"' in metrics_text
    assert 'error_code="payment.failed"' in metrics_text
    assert 'error_code="payment.delayed"' in metrics_text
    assert 'failure_kind="business_rejection"' in metrics_text
    assert 'failure_kind="dependency_error"' in metrics_text
    assert 'retryable="true"' in metrics_text
    assert "payment_request_duration_seconds_bucket" in metrics_text
    assert "payment_request_duration_seconds_count" in metrics_text
    assert "payment_events_published_total" in metrics_text
    assert 'event_type="payment-approved"' in metrics_text
    assert 'event_type="payment-failed"' in metrics_text
    assert 'result="success"' in metrics_text
    assert_no_high_cardinality_metric_labels(metrics_text)


def test_payment_event_dispatcher_publishes_pending_event_and_marks_published() -> None:
    producer = FakeKafkaProducer()
    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="14"),
        json={
            "reservationId": "res-14",
            "concertId": "concert-events",
            "seatId": "seat-A1",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    assert asyncio.run(dispatch_pending_events(producer)) == 1

    assert producer.sent[0][0] == "payment-approved"
    assert producer.sent[0][1]["reservationId"] == "res-14"
    assert producer.sent[0][1]["seatId"] == "seat-A1"
    assert dict(producer.sent[0][2])["correlation_id"] == producer.sent[0][1]["correlationId"].encode("utf-8")
    events = payment_events()
    assert events[0].publish_status == "published"
    assert events[0].publish_attempts == 1
    assert events[0].published_at is not None
    assert events[0].last_publish_error is None


def test_payment_event_dispatcher_loop_publishes_pending_events() -> None:
    stop_event = asyncio.Event()
    producer = StoppingKafkaProducer(stop_event)
    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="16"),
        json={
            "reservationId": "res-16",
            "concertId": "concert-events",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    asyncio.run(run_dispatcher_loop(stop_event, producer))

    assert producer.sent[0][0] == "payment-approved"
    events = payment_events()
    assert events[0].publish_status == "published"


def test_payment_event_dispatcher_failure_metric_preserves_error_flow() -> None:
    producer = FailingKafkaProducer()

    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="15"),
        json={
            "reservationId": "res-15",
            "concertId": "concert-metrics",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    with pytest.raises(RuntimeError, match="kafka publish failed"):
        asyncio.run(dispatch_pending_events(producer, max_attempts=1))

    events = payment_events()
    assert events[0].publish_status == "failed"
    assert events[0].publish_attempts == 1
    assert "kafka publish failed" in events[0].last_publish_error
    metrics_text = client.get("/metrics").text
    assert "payment_events_published_total" in metrics_text
    assert 'event_type="payment-approved"' in metrics_text
    assert 'result="failure"' in metrics_text
    assert_no_high_cardinality_metric_labels(metrics_text)


def auth_headers(role: str, user_id: str = "1") -> dict[str, str]:
    return {
        "X-User-Id": user_id,
        "X-User-Email": f"{role.lower()}@example.com",
        "X-User-Role": role,
        "X-Token-Id": f"token-{user_id}",
    }


def payment_events() -> list[PaymentEvent]:
    with SessionLocal() as db:
        return db.query(PaymentEvent).order_by(PaymentEvent.created_at).all()


async def dispatch_pending_events(producer: "FakeKafkaProducer | FailingKafkaProducer", *, max_attempts: int = 3) -> int:
    with SessionLocal() as db:
        dispatcher = PaymentEventDispatcher(
            db=db,
            telemetry=PaymentTelemetryRecorder(),
            max_attempts=max_attempts,
        )
        return await dispatcher.dispatch_pending(kafka_producer=producer)


async def run_dispatcher_loop(stop_event: asyncio.Event, producer: "StoppingKafkaProducer") -> None:
    await asyncio.wait_for(
        run_payment_event_dispatcher(
            stop_event,
            session_factory=SessionLocal,
            kafka_producer=producer,
            interval_seconds=0.01,
            batch_size=10,
        ),
        timeout=1,
    )


def assert_no_high_cardinality_metric_labels(metrics_text: str) -> None:
    forbidden_labels = (
        "request_id",
        "trace_id",
        "span_id",
        "correlation_id",
        "user_id",
        "payment_id",
        "reservation_id",
        "ticket_id",
        "raw_path",
    )
    for label in forbidden_labels:
        assert f"{label}=" not in metrics_text


class FakeKafkaProducer:
    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, list[tuple[str, bytes]]]] = []

    async def send_and_wait(self, topic: str, payload: dict, *, headers: list[tuple[str, bytes]]) -> None:
        self.sent.append((topic, payload, headers))


class StoppingKafkaProducer(FakeKafkaProducer):
    def __init__(self, stop_event: asyncio.Event) -> None:
        super().__init__()
        self._stop_event = stop_event

    async def send_and_wait(self, topic: str, payload: dict, *, headers: list[tuple[str, bytes]]) -> None:
        await super().send_and_wait(topic, payload, headers=headers)
        self._stop_event.set()


class FailingKafkaProducer:
    async def send_and_wait(self, topic: str, payload: dict, *, headers: list[tuple[str, bytes]]) -> None:
        raise RuntimeError("kafka publish failed")
