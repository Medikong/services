from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.main import create_app


def test_readyz_returns_json_503_when_database_connection_is_refused(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.setenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@127.0.0.1:55441/order_db",
    )
    monkeypatch.delenv("KAFKA_BOOTSTRAP_SERVERS", raising=False)

    # When
    with TestClient(create_app(), raise_server_exceptions=False) as client:
        response = client.get("/readyz")

    # Then
    assert response.status_code == 503
    assert response.headers["content-type"].startswith("application/json")
    assert response.json()["status"] == "not_ready"
    assert response.json()["checks"]["database_migration"] == "failed"
