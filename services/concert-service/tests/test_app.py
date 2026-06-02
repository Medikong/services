from fastapi import FastAPI
from fastapi.testclient import TestClient
from pytest import MonkeyPatch

from app import main as app_main
from app.config import Settings
from app.main import create_app


def test_create_app_returns_fastapi_app() -> None:
    app = create_app()

    assert isinstance(app, FastAPI)


def test_health_returns_service_status() -> None:
    client = TestClient(create_app())

    response = client.get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "concert-service"}


def test_healthz_returns_service_status() -> None:
    client = TestClient(create_app())

    response = client.get("/healthz")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "concert-service"}


def test_readyz_returns_readiness_checks() -> None:
    client = TestClient(create_app())

    response = client.get("/readyz")

    assert response.status_code == 200
    assert response.json() == {
        "status": "ready",
        "service": "concert-service",
        "checks": {
            "config": "ok",
            "database": "ok",
        },
    }


def test_readyz_returns_503_when_a_check_fails(monkeypatch: MonkeyPatch) -> None:
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
        "service": "concert-service",
        "checks": {
            "config": "ok",
            "database": "failed: OperationalError",
        },
    }


def test_metrics_returns_prometheus_text() -> None:
    client = TestClient(create_app())
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_requests_total" in response.text
    assert 'service="concert-service"' in response.text
    assert 'method="GET"' in response.text
    assert 'path="/healthz"' in response.text
    assert 'status="200"' in response.text


def test_settings_defaults(monkeypatch: MonkeyPatch) -> None:
    monkeypatch.delenv("SERVICE_NAME", raising=False)
    monkeypatch.delenv("PORT", raising=False)
    monkeypatch.delenv("DATABASE_URL", raising=False)

    settings = Settings()

    assert settings.service_name == "concert-service"
    assert settings.port == 8082
    assert settings.database_url == "sqlite:///./concert_service.db"
