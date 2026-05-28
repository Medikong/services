from fastapi import FastAPI
from fastapi.testclient import TestClient
from pytest import MonkeyPatch

from app.config import Settings
from app.main import create_app


def test_create_app_returns_fastapi_app() -> None:
    app = create_app()

    assert isinstance(app, FastAPI)


def test_health_returns_service_status() -> None:
    client = TestClient(create_app())

    response = client.get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "reservation-service"}


def test_settings_defaults(monkeypatch: MonkeyPatch) -> None:
    monkeypatch.delenv("SERVICE_NAME", raising=False)
    monkeypatch.delenv("PORT", raising=False)
    monkeypatch.delenv("DATABASE_URL", raising=False)

    settings = Settings()

    assert settings.service_name == "reservation-service"
    assert settings.port == 8083
    assert settings.database_url == "sqlite:///./reservation_service.db"
