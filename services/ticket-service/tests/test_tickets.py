from collections.abc import Generator
from contextlib import contextmanager
from dataclasses import dataclass, field
import asyncio

import pytest
from fastapi.testclient import TestClient
from kafka_utils import KafkaProducerOption
from server.ids import deterministic_uuid_string
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker
from sqlalchemy.pool import StaticPool

from app.auth import UserContext
from app import auth as auth_module
from app import database as database_module
from app.consumers import kafka_consumer as kafka_consumer_module
from app.consumers.kafka_consumer import consume_events
from app.database import Base, get_db
from app.kafka import get_kafka_producer
from app.main import app
import app.main as main_module
from app.routers import tickets as tickets_router_module
from app.services import ticket_service
import app.worker as worker_module


RESERVATION_ID = deterministic_uuid_string("ticket-service-test", "reservation", 1)
CONCERT_ID = deterministic_uuid_string("ticket-service-test", "concert", 1)
SEAT_ID = deterministic_uuid_string("ticket-service-test", "seat", 1)
PAYMENT_ID = deterministic_uuid_string("ticket-service-test", "payment", 1)
PAYMENT_APPROVED_EVENT_ID = deterministic_uuid_string("ticket-service-test", "payment-approved-event", 1)


def make_test_uuid(*parts: object) -> str:
    return deterministic_uuid_string("ticket-service-test", *parts)


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
    app.dependency_overrides[get_db] = override_get_db
    yield
    app.dependency_overrides.pop(get_kafka_producer, None)


# ── 단위 테스트 ────────────────────────────────────────────────

def test_issue_ticket_creates_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    response = client.post("/tickets/issue", json=ticket_issue_request())

    assert response.status_code == 200
    assert response.json()["reservationId"] == RESERVATION_ID
    assert response.json()["status"] == "ISSUED"


def test_duplicate_issue_returns_existing_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    first = client.post("/tickets/issue", json=ticket_issue_request())
    second = client.post("/tickets/issue", json=ticket_issue_request())

    assert first.json()["id"] == second.json()["id"]
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "tickets_issued_total", result="success", source="api")
    assert_metric_labels(metrics, "tickets_issued_total", result="duplicate", source="api")


def test_issue_ticket_publishes_ticket_issued_event(monkeypatch: pytest.MonkeyPatch) -> None:
    producer = FakeKafkaProducer()
    app.dependency_overrides[get_kafka_producer] = lambda: producer
    monkeypatch.setattr(ticket_service.s3, "upload_qr", lambda *args: None)
    monkeypatch.setattr(ticket_service.s3, "upload_pdf", lambda *args: None)

    client.post("/tickets/issue", json=ticket_issue_request())

    assert producer.sent[0][0] == "ticket-issued"
    assert producer.sent[0][1]["eventType"] == "ticket-issued"
    assert producer.sent[0][1]["ticketId"] == str(producer.sent[0][1]["sourceId"])
    assert producer.sent[0][1]["concertId"] == CONCERT_ID
    assert producer.sent[0][1]["seatId"] == SEAT_ID


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
    body = response.json()
    assert body["items"][0]["id"] == issued["id"]
    assert body["nextCursor"] is None


def test_list_my_tickets_applies_limit_and_returns_next_cursor(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = issue_tickets_for_user(monkeypatch, "1", 3)
    response = client.get("/tickets/me?limit=2", headers=user_headers(1))

    assert response.status_code == 200
    body = response.json()
    assert [ticket["id"] for ticket in body["items"]] == [issued[0]["id"], issued[1]["id"]]
    assert body["nextCursor"] == str(issued[1]["id"])


def test_list_my_tickets_uses_cursor_for_next_page(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = issue_tickets_for_user(monkeypatch, "1", 3)
    first_page = client.get("/tickets/me?limit=2", headers=user_headers(1)).json()
    second_page = client.get(
        f"/tickets/me?limit=2&cursor={first_page['nextCursor']}",
        headers=user_headers(1),
    )

    assert second_page.status_code == 200
    body = second_page.json()
    assert [ticket["id"] for ticket in body["items"]] == [issued[2]["id"]]
    assert body["nextCursor"] is None


def test_list_my_tickets_does_not_mix_other_user_tickets(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    user_one_tickets = issue_tickets_for_user(monkeypatch, "1", 2)
    issue_tickets_for_user(monkeypatch, "99", 2)
    response = client.get("/tickets/me?limit=10", headers=user_headers(1))

    assert response.status_code == 200
    body = response.json()
    assert [ticket["id"] for ticket in body["items"]] == [ticket["id"] for ticket in user_one_tickets]
    assert {ticket["userId"] for ticket in body["items"]} == {"1"}
    assert body["nextCursor"] is None


def test_list_my_tickets_records_query_and_response_trace_spans(monkeypatch: pytest.MonkeyPatch) -> None:
    issued = issue_tickets_for_user(monkeypatch, "1", 2)
    trace = RecordingTraceRecorder()
    db = TestingSessionLocal()

    try:
        response = ticket_service.list_my_tickets(
            db,
            UserContext(user_id="1", role="USER"),
            limit=1,
            trace=trace,
        )
    finally:
        db.close()

    assert response.items[0].id == issued[0]["id"]
    assert response.nextCursor == str(issued[0]["id"])
    assert trace.spans == [
        (
            "ticket.list.query",
            {
                "ticket.list.limit": 1,
                "ticket.list.cursor_present": False,
            },
        ),
        (
            "ticket.list.query.build",
            {
                "ticket.list.cursor_present": False,
            },
        ),
        (
            "ticket.list.query.execute",
            {
                "ticket.list.limit_plus_one": 2,
            },
        ),
        (
            "ticket.list.query.pool_checkout",
            {},
        ),
        (
            "ticket.list.response",
            {
                "ticket.list.item_count": 1,
                "ticket.list.has_next_cursor": True,
            },
        ),
    ]
    assert trace.events == [
        (
            "ticket.list.service.enter",
            {
                "ticket.list.limit": 1,
                "ticket.list.cursor_present": False,
            },
        ),
        (
            "ticket.list.query.pool_checkout.acquired",
            {},
        ),
        (
            "ticket.list.query.returned",
            {
                "ticket.list.row_count": 2,
                "ticket.list.limit_plus_one": 2,
            },
        ),
    ]


def test_get_user_context_records_dependency_trace_span(monkeypatch: pytest.MonkeyPatch) -> None:
    trace = RecordingTraceRecorder()
    monkeypatch.setattr(auth_module, "trace_recorder", lambda: trace)

    user = auth_module.get_user_context(x_user_id="user-1", x_user_role="user")

    assert user == UserContext(user_id="user-1", role="USER")
    assert trace.spans == [
        (
            "ticket.dependency.user_context",
            {
                "ticket.auth.user_id_present": True,
                "ticket.auth.role_present": True,
            },
        ),
    ]


def test_get_db_records_session_create_and_close_trace_spans(monkeypatch: pytest.MonkeyPatch) -> None:
    trace = RecordingTraceRecorder()
    session = FakeSession()
    monkeypatch.setattr(database_module, "trace_recorder", lambda: trace)
    monkeypatch.setattr(database_module, "SessionLocal", lambda: session)

    generator = database_module.get_db()
    db = next(generator)
    generator.close()

    assert db is session
    assert session.closed is True
    assert trace.spans == [
        ("ticket.dependency.db.session_create", {}),
        ("ticket.dependency.db.session_close", {}),
    ]


def test_list_my_tickets_async_experiment_records_route_threadpool_trace(monkeypatch: pytest.MonkeyPatch) -> None:
    issued = issue_tickets_for_user(monkeypatch, "1", 2)
    trace = RecordingTraceRecorder()
    monkeypatch.setattr(tickets_router_module, "trace_recorder", lambda: trace)
    db = TestingSessionLocal()

    try:
        response = asyncio.run(
            tickets_router_module.list_my_tickets_async_experiment(
                limit=1,
                cursor=None,
                db=db,
                user=UserContext(user_id="1", role="USER"),
            )
        )
    finally:
        db.close()

    assert response.items[0].id == issued[0]["id"]
    assert response.nextCursor == str(issued[0]["id"])
    assert trace.spans[:2] == [
        (
            "ticket.list.route.async_experiment",
            {
                "ticket.list.limit": 1,
                "ticket.list.cursor_present": False,
            },
        ),
        (
            "ticket.list.query",
            {
                "ticket.list.limit": 1,
                "ticket.list.cursor_present": False,
            },
        ),
    ]
    assert trace.events[0] == ("ticket.list.route.threadpool_call.start", {})
    assert trace.events[-1] == (
        "ticket.list.route.threadpool_call.end",
        {
            "ticket.list.item_count": 1,
            "ticket.list.has_next_cursor": True,
        },
    )


def test_user_cannot_get_other_user_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    issued = client.post("/tickets/issue", json=ticket_issue_request()).json()
    response = client.get(f"/tickets/{issued['id']}", headers=user_headers(99))

    assert response.status_code == 403


def test_payment_approved_event_issues_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    db = TestingSessionLocal()
    producer = FakeKafkaProducer()
    asyncio.run(ticket_service.handle_payment_approved(db, payment_approved_event(), producer))

    from app.models import Ticket
    ticket = db.query(Ticket).first()
    assert ticket is not None
    assert ticket.reservation_id == RESERVATION_ID
    assert producer.sent[0][2] == []
    assert producer.options_sent[0].correlation_id == "corr-1"
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "ticket_events_consumed_total", event_type="payment-approved", result="success", topic="payment-approved")
    assert_metric_labels(metrics, "ticket_events_published_total", event_type="ticket-issued", result="success")


def test_duplicate_payment_event_does_not_create_duplicate_ticket(monkeypatch: pytest.MonkeyPatch) -> None:
    _mock_kafka_and_s3(monkeypatch)

    db = TestingSessionLocal()
    producer = FakeKafkaProducer()
    asyncio.run(ticket_service.handle_payment_approved(db, payment_approved_event(), producer))
    asyncio.run(ticket_service.handle_payment_approved(db, payment_approved_event(), producer))

    from app.models import Ticket
    count = db.query(Ticket).count()
    assert count == 1
    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "ticket_events_consumed_total", event_type="payment-approved", result="duplicate", topic="payment-approved")


def test_issue_ticket_records_publish_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(ticket_service.s3, "upload_qr", lambda *args: None)
    monkeypatch.setattr(ticket_service.s3, "upload_pdf", lambda *args: None)
    db = TestingSessionLocal()

    with pytest.raises(RuntimeError, match="kafka unavailable"):
        asyncio.run(ticket_service.issue_ticket(db, ticket_issue_request_model(), FailingKafkaProducer()))

    metrics = client.get("/metrics").text
    assert_metric_labels(metrics, "ticket_events_published_total", event_type="ticket-issued", result="failure")
    assert_metric_labels(metrics, "tickets_issued_total", result="failure", source="api")


def test_kafka_event_handlers_bind_topic_outside_consumer(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, object, object]] = []

    class FakePaymentApprovedEventHandler:
        def __init__(self, db_session_factory: object, kafka_producer: FakeKafkaProducer) -> None:
            calls.append(("init", db_session_factory, kafka_producer))

        async def __call__(self, payload: dict) -> None:
            calls.append(("call", payload, None))

    monkeypatch.setattr(worker_module, "PaymentApprovedEventHandler", FakePaymentApprovedEventHandler)
    producer = FakeKafkaProducer()
    handlers = worker_module.kafka_event_handlers(producer)
    payload = payment_approved_event()
    asyncio.run(handlers["payment-approved"](payload))

    assert list(handlers) == ["payment-approved"]
    assert calls == [
        ("init", worker_module.SessionLocal, producer),
        ("call", payload, None),
    ]


def test_lifespan_creates_producer_and_disposes_engine_without_consumer(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    class LifespanKafkaProducer:
        def __init__(self) -> None:
            self.stopped = False

        async def start(self) -> None:
            calls.append("producer-start")

        async def stop(self) -> None:
            self.stopped = True
            calls.append("producer-stop")

    producer = LifespanKafkaProducer()

    def fake_create_producer() -> LifespanKafkaProducer:
        calls.append("create-producer")
        return producer

    monkeypatch.setattr(main_module, "create_producer", fake_create_producer)
    monkeypatch.setattr(main_module.engine, "dispose", lambda: calls.append("dispose"))

    assert app.state.kafka_producer is None

    with TestClient(app):
        assert app.state.kafka_producer is producer

    assert producer.stopped is True
    assert app.state.kafka_producer is None
    assert calls == [
        "create-producer",
        "producer-start",
        "producer-stop",
        "dispose",
    ]


def test_worker_creates_producer_awaits_consumer_and_disposes_engine(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[object] = []

    class WorkerKafkaProducer:
        def __init__(self) -> None:
            self.started = False
            self.stopped = False

        async def start(self) -> None:
            self.started = True
            calls.append("producer-start")

        async def stop(self) -> None:
            self.stopped = True
            calls.append("producer-stop")

    producer = WorkerKafkaProducer()

    def fake_create_producer() -> WorkerKafkaProducer:
        calls.append("create-producer")
        return producer

    async def fake_consume_events(stop_event: asyncio.Event, **kwargs: object) -> None:
        calls.append(("consumer-producer-started", producer.started, list(kwargs["handlers"])))
        stop_event.set()

    monkeypatch.setattr(worker_module, "_install_signal_handlers", lambda stop_event: None)
    monkeypatch.setattr(worker_module.models.Base.metadata, "create_all", lambda bind: calls.append("create-all"))
    monkeypatch.setattr(worker_module, "create_producer", fake_create_producer)
    monkeypatch.setattr(worker_module, "consume_events", fake_consume_events)
    monkeypatch.setattr(worker_module.engine, "dispose", lambda: calls.append("dispose"))

    asyncio.run(worker_module.run_worker())

    assert producer.stopped is True
    assert calls == [
        "create-all",
        "create-producer",
        "producer-start",
        ("consumer-producer-started", True, ["payment-approved"]),
        "producer-stop",
        "dispose",
    ]


def test_worker_cancels_consumer_after_shutdown_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    class WorkerKafkaProducer:
        async def start(self) -> None:
            calls.append("producer-start")

        async def stop(self) -> None:
            calls.append("producer-stop")

    async def fake_consume_events(stop_event: asyncio.Event, **kwargs: object) -> None:
        calls.append("consumer-start")
        try:
            await asyncio.Event().wait()
        except asyncio.CancelledError:
            calls.append("consumer-cancelled")
            raise

    monkeypatch.setattr(worker_module, "_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS", 0.01)
    monkeypatch.setattr(worker_module, "_install_signal_handlers", lambda stop_event: stop_event.set())
    monkeypatch.setattr(worker_module.models.Base.metadata, "create_all", lambda bind: None)
    monkeypatch.setattr(worker_module, "create_producer", lambda: WorkerKafkaProducer())
    monkeypatch.setattr(worker_module, "consume_events", fake_consume_events)
    monkeypatch.setattr(worker_module.engine, "dispose", lambda: calls.append("dispose"))

    asyncio.run(worker_module.run_worker())

    assert calls == [
        "producer-start",
        "consumer-start",
        "consumer-cancelled",
        "producer-stop",
        "dispose",
    ]


def test_payment_approved_event_handler_owns_db_session(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[object, dict, FakeKafkaProducer]] = []

    class FakeSession:
        def __init__(self) -> None:
            self.closed = False

        def close(self) -> None:
            self.closed = True

    async def fake_handle_payment_approved(db: object, payload: dict, kafka_producer: FakeKafkaProducer) -> None:
        calls.append((db, payload, kafka_producer))

    session = FakeSession()
    producer = FakeKafkaProducer()
    payload = payment_approved_event()
    monkeypatch.setattr(ticket_service, "handle_payment_approved", fake_handle_payment_approved)

    handler = ticket_service.PaymentApprovedEventHandler(lambda: session, producer)
    asyncio.run(handler(payload))

    assert calls == [(session, payload, producer)]
    assert session.closed is True


def test_consume_events_uses_injected_config_and_handlers() -> None:
    created: list[FakeConsumer] = []
    handled: list[dict] = []
    payload = payment_approved_event()

    async def handle_event(message_payload: dict) -> None:
        handled.append(message_payload)

    def consumer_factory(*topics: str, **kwargs: object) -> FakeConsumer:
        consumer = FakeConsumer(topics=topics, kwargs=kwargs, messages=[FakeMessage("payment-approved", payload)])
        created.append(consumer)
        return consumer

    asyncio.run(
        consume_events(
            asyncio.Event(),
            bootstrap_servers="kafka:9092",
            group_id="ticket-service",
            service_name="ticket-service",
            handlers={"payment-approved": handle_event},
            consumer_factory=consumer_factory,
        )
    )

    assert created[0].topics == ("payment-approved",)
    assert created[0].kwargs["bootstrap_servers"] == "kafka:9092"
    assert created[0].kwargs["group_id"] == "ticket-service"
    assert created[0].started is True
    assert created[0].stopped is True
    assert handled == [payload]


def test_consume_events_passes_trace_headers_to_consumer_span(monkeypatch: pytest.MonkeyPatch) -> None:
    payload = payment_approved_event()
    trace_headers = [
        ("traceparent", b"00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01"),
        ("tracestate", b"vendor=value"),
    ]
    observed_headers: list[list[tuple[str, bytes]]] = []

    @contextmanager
    def fake_start_consumer_span(message: FakeMessage):
        observed_headers.append(list(message.headers))
        yield object()

    monkeypatch.setattr(kafka_consumer_module, "start_consumer_span", fake_start_consumer_span)

    async def handle_event(message_payload: dict) -> None:
        assert message_payload == payload

    def consumer_factory(*topics: str, **kwargs: object) -> FakeConsumer:
        return FakeConsumer(
            topics=topics,
            kwargs=kwargs,
            messages=[FakeMessage("payment-approved", payload, headers=trace_headers)],
        )

    asyncio.run(
        kafka_consumer_module.consume_events(
            asyncio.Event(),
            bootstrap_servers="kafka:9092",
            group_id="ticket-service",
            service_name="ticket-service",
            handlers={"payment-approved": handle_event},
            consumer_factory=consumer_factory,
        )
    )

    assert observed_headers == [trace_headers]


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
    assert "http_server_request_duration_seconds" in response.text
    assert "http_server_active_requests" in response.text
    assert "service_ready" in response.text
    assert 'service_name="ticket-service"' in response.text
    assert 'http_request_method="GET"' in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_response_status_code="200"' in response.text


# ── 헬퍼 ──────────────────────────────────────────────────────

def ticket_issue_request(
    *,
    reservation_id: str = RESERVATION_ID,
    user_id: str = "1",
    concert_id: str = CONCERT_ID,
    seat_id: str = SEAT_ID,
) -> dict:
    return {
        "reservationId": reservation_id,
        "userId": user_id,
        "concertId": concert_id,
        "seatId": seat_id,
    }


def ticket_issue_request_model():
    from app.schemas import TicketIssueRequest

    return TicketIssueRequest.model_validate(ticket_issue_request())


def payment_approved_event() -> dict:
    return {
        "eventId": PAYMENT_APPROVED_EVENT_ID,
        "eventType": "payment-approved",
        "userId": "1",
        "sourceId": PAYMENT_ID,
        "reservationId": RESERVATION_ID,
        "concertId": CONCERT_ID,
        "seatId": SEAT_ID,
        "paymentId": PAYMENT_ID,
        "amount": 50000,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "payment-service",
        "correlationId": "corr-1",
    }


def user_headers(user_id: int | str) -> dict[str, str]:
    return {"X-User-Id": str(user_id), "X-User-Role": "USER"}


def _mock_kafka_and_s3(monkeypatch: pytest.MonkeyPatch) -> None:
    app.dependency_overrides[get_kafka_producer] = lambda: FakeKafkaProducer()
    monkeypatch.setattr(ticket_service.s3, "upload_qr", lambda *args: None)
    monkeypatch.setattr(ticket_service.s3, "upload_pdf", lambda *args: None)


def issue_tickets_for_user(monkeypatch: pytest.MonkeyPatch, user_id: str, count: int) -> list[dict]:
    _mock_kafka_and_s3(monkeypatch)
    tickets = []
    for index in range(count):
        tickets.append(client.post(
            "/tickets/issue",
            json=ticket_issue_request(
                reservation_id=make_test_uuid("reservation", user_id, index),
                user_id=user_id,
                concert_id=make_test_uuid("concert", index),
                seat_id=make_test_uuid("seat", index),
            ),
        ).json())
    return tickets


def assert_metric_labels(metrics: str, metric_name: str, **labels: str) -> None:
    label_fragments = [f'{key}="{value}"' for key, value in {"service_name": "ticket-service", **labels}.items()]
    assert any(line.startswith(metric_name + "{") and all(fragment in line for fragment in label_fragments) for line in metrics.splitlines())


class FakeKafkaProducer:
    def __init__(self) -> None:
        self.sent: list[tuple[str, dict, list[tuple[str, bytes]]]] = []
        self.options_sent: list[RecordedKafkaOptions] = []

    async def send_and_wait(
        self,
        topic: str,
        payload: dict,
        *producer_options: KafkaProducerOption,
        headers: list[tuple[str, bytes]] | None = None,
    ) -> None:
        options = RecordedKafkaOptions()
        for producer_option in producer_options:
            producer_option(options)
        self.options_sent.append(options)
        self.sent.append((topic, payload, list(headers or [])))


class FailingKafkaProducer:
    async def send_and_wait(
        self,
        topic: str,
        payload: dict,
        *producer_options: KafkaProducerOption,
        headers: list[tuple[str, bytes]] | None = None,
    ) -> None:
        raise RuntimeError("kafka unavailable")


class FakeSession:
    def __init__(self) -> None:
        self.closed = False

    def close(self) -> None:
        self.closed = True


class RecordingTraceRecorder:
    def __init__(self) -> None:
        self.spans: list[tuple[str, dict[str, object]]] = []
        self.events: list[tuple[str, dict[str, object]]] = []

    def span(self, name: str, attributes: dict[str, object] | None = None):
        self.spans.append((name, attributes or {}))

        @contextmanager
        def child_span() -> Generator[None, None, None]:
            yield

        return child_span()

    def attribute(self, key: str, value: object) -> None:
        return None

    def event(self, name: str, attributes: dict[str, object] | None = None) -> None:
        self.events.append((name, attributes or {}))


@dataclass
class RecordedKafkaOptions:
    trace_context: dict | None = None
    trace_carrier: dict | None = None
    correlation_id: str | None = None
    span_name: str | None = None
    span_attributes: dict[str, object] = field(default_factory=dict)


class FakeMessage:
    def __init__(self, topic: str, value: dict, *, headers: list[tuple[str, bytes]] | None = None) -> None:
        self.topic = topic
        self.value = value
        self.headers = headers or []
        self.partition = 0
        self.offset = 0


class FakeConsumer:
    def __init__(self, *, topics: tuple[str, ...], kwargs: dict[str, object], messages: list[FakeMessage]) -> None:
        self.topics = topics
        self.kwargs = kwargs
        self.messages = messages
        self.started = False
        self.stopped = False

    async def start(self) -> None:
        self.started = True

    async def stop(self) -> None:
        self.stopped = True

    def __aiter__(self) -> "FakeConsumer":
        return self

    async def __anext__(self) -> FakeMessage:
        if not self.messages:
            raise StopAsyncIteration
        return self.messages.pop(0)
