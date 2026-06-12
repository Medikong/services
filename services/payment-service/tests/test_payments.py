import asyncio
import os
from contextlib import contextmanager
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
import app.main as app_main  # noqa: E402
import app.routes.payments as payment_routes  # noqa: E402
import app.services.payment_events as payment_events_module  # noqa: E402
from app.metrics.recorder import PaymentTelemetryRecorder  # noqa: E402
from app.models import PaymentEvent  # noqa: E402
from app.services.payment_events import PaymentEventDispatcher, run_payment_event_dispatcher  # noqa: E402
from observability import TraceContext  # noqa: E402


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


def test_lifespan_creates_producer_starts_dispatcher_and_disposes_engine(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[object] = []

    class LifespanKafkaProducer:
        def __init__(self) -> None:
            self.started = False
            self.stopped = False

        async def start(self) -> None:
            self.started = True
            calls.append("producer-start")

        async def stop(self) -> None:
            self.stopped = True
            calls.append("producer-stop")

    producer = LifespanKafkaProducer()

    def fake_create_producer() -> LifespanKafkaProducer:
        calls.append("create-producer")
        return producer

    async def fake_run_payment_event_dispatcher(stop_event: asyncio.Event, **kwargs: object) -> None:
        calls.append(("dispatcher-producer-started", kwargs["kafka_producer"].started))
        await stop_event.wait()
        calls.append("dispatcher-stopped")

    monkeypatch.setattr(app_main, "create_producer", fake_create_producer)
    monkeypatch.setattr(app_main, "run_payment_event_dispatcher", fake_run_payment_event_dispatcher)
    monkeypatch.setattr(app_main.engine, "dispose", lambda: calls.append("dispose"))

    assert app.state.kafka_producer is None

    with TestClient(app):
        assert app.state.kafka_producer is producer

    assert producer.stopped is True
    assert app.state.kafka_producer is None
    assert calls == [
        "create-producer",
        "producer-start",
        ("dispatcher-producer-started", True),
        "dispatcher-stopped",
        "producer-stop",
        "dispose",
    ]


def test_lifespan_cancels_dispatcher_after_shutdown_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    class LifespanKafkaProducer:
        async def start(self) -> None:
            calls.append("producer-start")

        async def stop(self) -> None:
            calls.append("producer-stop")

    async def fake_run_payment_event_dispatcher(stop_event: asyncio.Event, **kwargs: object) -> None:
        calls.append("dispatcher-start")
        try:
            await asyncio.Event().wait()
        except asyncio.CancelledError:
            calls.append("dispatcher-cancelled")
            raise

    monkeypatch.setattr(app_main, "_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS", 0.01)
    monkeypatch.setattr(app_main, "create_producer", lambda: LifespanKafkaProducer())
    monkeypatch.setattr(app_main, "run_payment_event_dispatcher", fake_run_payment_event_dispatcher)
    monkeypatch.setattr(app_main.engine, "dispose", lambda: calls.append("dispose"))

    with TestClient(app):
        assert app.state.payment_event_dispatcher_task is not None

    assert app.state.payment_event_dispatcher_task is None
    assert app.state.payment_event_dispatcher_stop_event is None
    assert calls == [
        "producer-start",
        "dispatcher-start",
        "dispatcher-cancelled",
        "producer-stop",
        "dispose",
    ]


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


def test_payment_event_dispatcher_wraps_outbox_work_in_trace_spans(monkeypatch: pytest.MonkeyPatch) -> None:
    producer = FakeKafkaProducer()
    span_names: list[str] = []

    @contextmanager
    def fake_start_trace_span(name: str, attributes: dict[str, object] | None = None):
        span_names.append(name)
        yield

    monkeypatch.setattr(payment_events_module, "start_trace_span", fake_start_trace_span)
    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="trace-span-user"),
        json={
            "reservationId": "res-trace-span",
            "concertId": "concert-events",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    assert asyncio.run(dispatch_pending_events(producer)) == 1
    assert span_names == ["payment.outbox.dispatch_pending", "payment.outbox.dispatch_event"]


def test_payment_outbox_preserves_trace_context_for_kafka_headers(monkeypatch: pytest.MonkeyPatch) -> None:
    trace_context = sample_trace_context()
    monkeypatch.setattr(payment_routes, "capture_current_trace_context", lambda: trace_context)
    producer = FakeKafkaProducer()

    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="trace-user"),
        json={
            "reservationId": "res-trace",
            "concertId": "concert-trace",
            "seatId": "seat-A1",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    events = payment_events()
    assert events[0].trace_context == trace_context.as_dict()
    assert "traceparent" not in events[0].payload
    assert "tracestate" not in events[0].payload

    assert asyncio.run(dispatch_pending_events(producer)) == 1
    headers = dict(producer.sent[0][2])
    traceparent = headers["traceparent"].decode("utf-8")
    assert traceparent.startswith(f"00-{trace_context.trace_id}-")
    assert traceparent.endswith("-01")
    assert traceparent != trace_context.carrier["traceparent"]
    assert headers["tracestate"] == trace_context.carrier["tracestate"].encode("utf-8")
    assert headers["correlation_id"] == producer.sent[0][1]["correlationId"].encode("utf-8")


def test_payment_event_dispatcher_starts_producer_span_from_outbox_trace_context(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    trace_context = sample_trace_context()
    monkeypatch.setattr(payment_routes, "capture_current_trace_context", lambda: trace_context)
    producer = FakeKafkaProducer()
    producer_spans: list[dict[str, object]] = []

    @contextmanager
    def fake_start_producer_span(
        topic: str,
        *,
        carrier: dict[str, str] | None = None,
        name: str | None = None,
        attributes: dict[str, object] | None = None,
    ):
        producer_spans.append(
            {
                "topic": topic,
                "carrier": carrier,
                "name": name,
                "attributes": attributes,
            }
        )
        yield

    monkeypatch.setattr(payment_events_module, "start_producer_span", fake_start_producer_span)
    response = client.post(
        "/payments",
        headers=auth_headers("CUSTOMER", user_id="trace-producer-user"),
        json={
            "reservationId": "res-trace-producer",
            "concertId": "concert-trace",
            "seatId": "seat-A1",
            "amount": 50000,
            "method": "mock",
            "simulation": "approve",
        },
    )

    assert response.status_code == 201
    assert asyncio.run(dispatch_pending_events(producer)) == 1
    assert producer_spans == [
        {
            "topic": "payment-approved",
            "carrier": trace_context.carrier,
            "name": None,
            "attributes": {"payment.event_type": "payment-approved"},
        }
    ]


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


def sample_trace_context() -> TraceContext:
    return TraceContext(
        carrier={
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
        },
        trace_id="4f3b2c1a9d8e7f60123456789abcdef0",
        span_id="6f1a2b3c4d5e6f70",
    )


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
