from datetime import UTC, datetime
from time import perf_counter

import pytest
from fastapi.testclient import TestClient

from app.catalog import CatalogReadiness, DropDetail, DropStatus, Product
from app.main import create_app

CATALOG = (
    DropDetail(
        id="drop-001",
        title="DropMong July Limited Drop",
        status=DropStatus.OPEN,
        opens_at=datetime(2026, 7, 3, 10, 0, tzinfo=UTC),
        closes_at=datetime(2026, 7, 10, 10, 0, tzinfo=UTC),
        description="한정 수량으로 판매되는 DropMong 첫 번째 공개 드롭입니다.",
        products=(
            Product(
                id="product-001",
                name="DropMong Starter Kit",
                price=50000,
                remaining_quantity=42,
                inventory_version=0,
            ),
        ),
    ),
    DropDetail(
        id="drop-sold-out-001",
        title="DropMong Sold Out Scenario Drop",
        status=DropStatus.OPEN,
        opens_at=datetime(2026, 7, 3, 10, 0, tzinfo=UTC),
        closes_at=datetime(2026, 7, 10, 10, 0, tzinfo=UTC),
        description="품절과 동시성 시나리오 검증을 위한 독립 드롭입니다.",
        products=(
            Product(
                id="product-sold-out-001",
                name="DropMong Concurrency Kit",
                price=50000,
                remaining_quantity=42,
                inventory_version=0,
            ),
        ),
    ),
)


class CatalogRepositoryStub:
    def __init__(self, *, ready: bool = True) -> None:
        self._ready = ready

    async def list_drops(self) -> tuple[DropDetail, ...]:
        return CATALOG

    async def get_drop(self, drop_id: str) -> DropDetail | None:
        return next((drop for drop in CATALOG if drop.id == drop_id), None)

    async def readiness(self) -> CatalogReadiness:
        return (
            CatalogReadiness.READY
            if self._ready
            else CatalogReadiness.MIGRATION_REQUIRED
        )


def _client(*, ready: bool = True) -> TestClient:
    return TestClient(create_app(repository=CatalogRepositoryStub(ready=ready)))


def test_healthz_returns_catalog_service_identity() -> None:
    client = _client()

    response = client.get("/healthz")

    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "catalog-service"


def test_healthz_echoes_request_id_header() -> None:
    client = _client()

    response = client.get("/healthz", headers={"X-Request-Id": "catalog-trace-smoke"})

    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "catalog-trace-smoke"


def test_readyz_returns_ready_catalog_check() -> None:
    client = _client()

    response = client.get("/readyz")

    assert response.status_code == 200
    assert response.json()["status"] == "ready"
    assert response.json()["checks"] == {"catalog": "ok"}


def test_metrics_exposes_common_http_histogram_contract() -> None:
    client = _client()
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert "# TYPE http_server_request_duration_seconds histogram" in response.text
    assert 'service_name="catalog-service"' in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_route_kind="probe"' in response.text
    assert 'http_response_status_code="200"' in response.text
    assert _service_ready_value(response.text) == 0.0
    assert 'service_version="0.1.0"' in response.text
    assert 'service_environment="local"' in response.text

    assert client.get("/readyz").status_code == 200
    assert _service_ready_value(client.get("/metrics").text) == 1.0


def test_list_drops_returns_open_drop_with_product() -> None:
    client = _client()

    response = client.get("/drops", params={"limit": 10})

    assert response.status_code == 200
    body = response.json()
    assert body["data"][0]["id"] == "drop-001"
    assert body["data"][0]["status"] == "OPEN"
    assert body["data"][0]["products"][0]["id"] == "product-001"
    assert body["data"][0]["products"][0]["price"] == 50000
    assert body["pageInfo"] == {"nextCursor": None, "hasNext": False}


def test_get_drop_returns_selected_drop_detail() -> None:
    client = _client()

    response = client.get("/drops/drop-001")

    assert response.status_code == 200
    body = response.json()
    assert body["data"]["id"] == "drop-001"
    assert body["data"]["description"]
    assert body["data"]["products"][0]["remainingQuantity"] == 42


def test_get_drop_returns_sold_out_scenario_drop_detail() -> None:
    client = _client()

    response = client.get("/drops/drop-sold-out-001")

    assert response.status_code == 200
    body = response.json()
    assert body["data"]["id"] == "drop-sold-out-001"
    assert body["data"]["products"][0]["id"] == "product-sold-out-001"
    assert body["data"]["products"][0]["remainingQuantity"] == 42


def test_get_drop_returns_404_when_drop_is_unknown() -> None:
    client = _client()

    response = client.get("/drops/unknown-drop")

    assert response.status_code == 404


def test_readyz_fails_when_catalog_migration_is_missing() -> None:
    client = _client(ready=False)

    response = client.get("/readyz")

    assert response.status_code == 503
    assert response.json()["checks"] == {"catalog": "migration_required"}
    assert _service_ready_value(client.get("/metrics").text) == 0.0


def _service_ready_value(metrics_text: str) -> float:
    line = next(
        line for line in metrics_text.splitlines() if line.startswith("service_ready{")
    )
    return float(line.rsplit(" ", maxsplit=1)[1])


def test_readyz_returns_bounded_503_when_database_connection_is_refused(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@127.0.0.1:1/catalog_db",
    )
    started_at = perf_counter()

    with TestClient(create_app(), raise_server_exceptions=False) as client:
        response = client.get("/readyz")

    assert perf_counter() - started_at < 2
    assert response.status_code == 503
    assert response.json()["status"] == "not_ready"
    assert response.json()["checks"] == {
        "catalog": "migration_required",
        "database": "unavailable",
    }
