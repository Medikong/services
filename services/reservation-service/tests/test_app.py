from fastapi import FastAPI
from fastapi.testclient import TestClient
from pytest import MonkeyPatch

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
