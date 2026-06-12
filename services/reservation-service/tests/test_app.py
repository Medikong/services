import asyncio
from contextlib import contextmanager

from fastapi import FastAPI
from fastapi.testclient import TestClient
from pytest import MonkeyPatch

from app.consumers import kafka_consumer as kafka_consumer_module
from app import main as app_main
from app.config import Settings
from app.main import create_app


def test_create_app_returns_fastapi_app() -> None:
    """앱 팩토리가 FastAPI 애플리케이션을 생성하는지 검증한다."""
    app = create_app()

    assert isinstance(app, FastAPI)


def test_kafka_producer_is_created_inside_lifespan(monkeypatch: MonkeyPatch) -> None:
    calls: list[str] = []

    class FakeKafkaProducer:
        def __init__(self) -> None:
            self.started = False
            self.stopped = False

        async def start(self) -> None:
            self.started = True
            calls.append("producer-start")

        async def stop(self) -> None:
            self.stopped = True
            calls.append("producer-stop")

    created: list[FakeKafkaProducer] = []

    def fake_create_producer() -> FakeKafkaProducer:
        producer = FakeKafkaProducer()
        created.append(producer)
        return producer

    monkeypatch.setattr(app_main, "create_producer", fake_create_producer)
    monkeypatch.setattr(app_main.engine, "dispose", lambda: calls.append("dispose"))

    app = create_app()

    assert created == []
    assert app.state.kafka_producer is None

    with TestClient(app):
        assert len(created) == 1
        assert created[0].started is True
        assert app.state.kafka_producer is created[0]

    assert created[0].stopped is True
    assert app.state.kafka_producer is None
    assert calls == ["producer-start", "producer-stop", "dispose"]


def test_health_returns_service_status() -> None:
    """기본 health 엔드포인트가 서비스 정상 상태를 반환하는지 검증한다."""
    client = TestClient(create_app())

    response = client.get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "reservation-service"}


def test_healthz_returns_service_status() -> None:
    """Kubernetes 스타일 healthz 엔드포인트가 서비스 정상 상태를 반환하는지 검증한다."""
    client = TestClient(create_app())

    response = client.get("/healthz")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "reservation-service"}


def test_readyz_returns_readiness_checks() -> None:
    """readyz 엔드포인트가 준비 상태와 개별 점검 결과를 반환하는지 검증한다."""
    client = TestClient(create_app())

    response = client.get("/readyz")

    assert response.status_code == 200
    assert response.json() == {
        "status": "ready",
        "service": "reservation-service",
        "checks": {
            "config": "ok",
            "database": "ok",
        },
    }


def test_readyz_returns_503_when_a_check_fails(monkeypatch: MonkeyPatch) -> None:
    """준비 상태 점검 중 하나가 실패하면 readyz가 503을 반환하는지 검증한다."""
    monkeypatch.setattr(
        app_main,
        "_readiness_checks",
        lambda: {
            "config": lambda: "ok",
            "database": lambda: "failed: OperationalError",
        },
    )
    client = TestClient(create_app())

    response = client.get("/readyz")

    assert response.status_code == 503
    assert response.json() == {
        "status": "not_ready",
        "service": "reservation-service",
        "checks": {
            "config": "ok",
            "database": "failed: OperationalError",
        },
    }


def test_metrics_returns_prometheus_text() -> None:
    """metrics 엔드포인트가 Prometheus 텍스트 형식 지표를 반환하는지 검증한다."""
    client = TestClient(create_app())
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_server_request_duration_seconds" in response.text
    assert "http_server_active_requests" in response.text
    assert "service_ready" in response.text
    assert 'service_name="reservation-service"' in response.text
    assert 'http_request_method="GET"' in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_response_status_code="200"' in response.text


def test_settings_defaults(monkeypatch: MonkeyPatch) -> None:
    """환경 변수가 없을 때 예약 서비스 기본 설정값이 적용되는지 검증한다."""
    monkeypatch.delenv("SERVICE_NAME", raising=False)
    monkeypatch.delenv("PORT", raising=False)
    monkeypatch.delenv("DATABASE_URL", raising=False)

    settings = Settings()

    assert settings.service_name == "reservation-service"
    assert settings.port == 8083
    assert settings.database_url == "sqlite:///./reservation_service.db"


def test_ticket_issued_consumer_passes_trace_headers_to_consumer_span(monkeypatch: MonkeyPatch) -> None:
    """ticket-issued 소비 처리가 Kafka trace header를 consumer span에 넘기는지 검증한다."""
    trace_headers = [
        ("traceparent", b"00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01"),
        ("tracestate", b"vendor=value"),
    ]
    observed_headers: list[list[tuple[str, bytes]]] = []
    confirmed_reservations: list[str] = []

    @contextmanager
    def fake_start_consumer_span(message: FakeMessage):
        observed_headers.append(list(message.headers))
        yield object()

    class FakeReservationCommandService:
        def __init__(self, db: object) -> None:
            self.db = db

        def confirm_reservation(self, reservation_id: str) -> None:
            confirmed_reservations.append(reservation_id)

    def fake_consumer_factory(*topics: str, **kwargs: object) -> FakeConsumer:
        return FakeConsumer(
            topics=topics,
            kwargs=kwargs,
            messages=[
                FakeMessage(
                    "ticket-issued",
                    {"eventType": "ticket-issued", "reservationId": "rsv-trace"},
                    headers=trace_headers,
                )
            ],
        )

    monkeypatch.setattr(kafka_consumer_module, "AIOKafkaConsumer", fake_consumer_factory)
    monkeypatch.setattr(kafka_consumer_module, "start_consumer_span", fake_start_consumer_span)
    monkeypatch.setattr(
        "app.services.reservations.ReservationCommandService",
        FakeReservationCommandService,
    )

    asyncio.run(
        kafka_consumer_module.consume_ticket_issued(
            asyncio.Event(),
            bootstrap_servers="kafka:9092",
            group_id="reservation-service",
            topic="ticket-issued",
            session_factory=FakeSession,
        )
    )

    assert observed_headers == [trace_headers]
    assert confirmed_reservations == ["rsv-trace"]


class FakeMessage:
    def __init__(self, topic: str, value: dict, *, headers: list[tuple[str, bytes]] | None = None) -> None:
        self.topic = topic
        self.value = value
        self.headers = headers or []
        self.partition = 0
        self.offset = 1


class FakeConsumer:
    def __init__(self, *, topics: tuple[str, ...], kwargs: dict[str, object], messages: list[FakeMessage]) -> None:
        self.topics = topics
        self.kwargs = kwargs
        self.messages = list(messages)
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


class FakeSession:
    def __enter__(self) -> "FakeSession":
        return self

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        return None
