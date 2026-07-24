import json

import pytest
from fastapi.testclient import TestClient

from app.catalog import (
    PRODUCTS_FOR_SALE,
    ProductCatalogConfigurationError,
    catalog_from_env,
)
from app.db import resources_from_env
from app.main import create_app
from app.postgres import PostgresOrderRepository
from app.store import OrderStore


def test_catalog_from_env_retains_defaults_and_appends_extra_product() -> None:
    # Given
    env = {
        "ORDER_EXTRA_PRODUCTS_JSON": json.dumps(
            [
                {
                    "drop_id": "drop-opaque-001",
                    "product_id": "product-opaque-001",
                    "unit_price": 73500,
                },
            ],
        ),
    }

    # When
    catalog = catalog_from_env(env)

    # Then
    assert catalog[:2] == PRODUCTS_FOR_SALE
    assert catalog[2].drop_id == "drop-opaque-001"
    assert catalog[2].product_id == "product-opaque-001"
    assert catalog[2].unit_price == 73500


def test_resources_from_env_passes_extra_catalog_to_in_memory_repository(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv(
        "ORDER_EXTRA_PRODUCTS_JSON",
        json.dumps(
            [
                {
                    "drop_id": "drop-opaque-002",
                    "product_id": "product-opaque-002",
                    "unit_price": 82000,
                },
            ],
        ),
    )

    # When
    resources = resources_from_env()

    # Then
    assert isinstance(resources.repository, OrderStore)
    assert resources.repository._catalog[-1].product_id == "product-opaque-002"


def test_resources_from_env_passes_extra_catalog_to_postgres_repository(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.setenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@127.0.0.1:59999/order_db",
    )
    monkeypatch.setenv(
        "ORDER_EXTRA_PRODUCTS_JSON",
        json.dumps(
            [
                {
                    "drop_id": "drop-opaque-003",
                    "product_id": "product-opaque-003",
                    "unit_price": 91000,
                },
            ],
        ),
    )

    # When
    resources = resources_from_env()

    # Then
    assert isinstance(resources.repository, PostgresOrderRepository)
    assert resources.repository._catalog[-1].product_id == "product-opaque-003"


def test_configured_product_is_accepted_by_real_in_memory_app_composition(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv(
        "ORDER_EXTRA_PRODUCTS_JSON",
        json.dumps(
            [
                {
                    "drop_id": "drop-opaque-004",
                    "product_id": "product-opaque-004",
                    "unit_price": 99000,
                },
            ],
        ),
    )

    # When
    with TestClient(create_app()) as client:
        response = client.post(
            "/orders",
            headers={
                "X-User-Id": "00000000-0000-4000-8000-000000000001",
                "X-User-Role": "CUSTOMER",
                "Idempotency-Key": "order-opaque-004",
            },
            json={
                "dropId": "drop-opaque-004",
                "productId": "product-opaque-004",
                "quantity": 1,
            },
        )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["amount"] == 99000


def test_invalid_extra_product_config_fails_before_app_serves(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.setenv("ORDER_EXTRA_PRODUCTS_JSON", "not-json")

    # When / Then
    with pytest.raises(ProductCatalogConfigurationError):
        create_app()
