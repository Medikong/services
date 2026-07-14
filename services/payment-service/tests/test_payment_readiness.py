from time import monotonic

import pytest
from fastapi.testclient import TestClient

from app.main import create_app


def test_readyz_returns_bounded_503_when_database_is_unreachable(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.setenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@127.0.0.1:1/payment_db",
    )
    client = TestClient(create_app(), raise_server_exceptions=False)

    # When
    started_at = monotonic()
    response = client.get("/readyz")
    elapsed_seconds = monotonic() - started_at

    # Then
    assert response.status_code == 503
    assert response.json()["status"] == "not_ready"
    assert response.json()["checks"]["database_schema"] == "unreachable"
    assert elapsed_seconds < 3
