from fastapi.testclient import TestClient

from app.catalog import ProductForSale
from app.main import create_app
from app.models import DropId, ProductId
from app.store import InventorySeed, OrderStore


def test_healthz_echoes_request_id_header() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.get("/healthz", headers={"X-Request-Id": "order-trace-smoke"})

    # Then
    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "order-trace-smoke"


def test_create_order_increments_orders_created_metric() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-metric-created-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    metrics_response = client.get("/metrics")

    # Then
    assert create_response.status_code == 201
    assert metrics_response.status_code == 200
    assert (
        'orders_created_total{service_name="order-service",'
        'service_version="0.1.0",service_environment="local"} 1\n'
    ) in metrics_response.text


def test_create_order_increments_sold_out_metric_when_stock_is_exhausted() -> None:
    # Given
    catalog = (
        ProductForSale(
            drop_id=DropId("drop-limited-metric"),
            product_id=ProductId("product-limited-metric"),
            unit_price=50000,
        ),
    )
    inventory = (
        InventorySeed(
            DropId("drop-limited-metric"),
            ProductId("product-limited-metric"),
            1,
        ),
    )
    client = TestClient(create_app(OrderStore(catalog, inventory)))

    # When
    first_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-metric-sold-out-001",
        },
        json={
            "dropId": "drop-limited-metric",
            "productId": "product-limited-metric",
            "quantity": 1,
        },
    )
    second_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-002",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-metric-sold-out-002",
        },
        json={
            "dropId": "drop-limited-metric",
            "productId": "product-limited-metric",
            "quantity": 1,
        },
    )
    metrics_response = client.get("/metrics")

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409
    assert "product sold out" in second_response.text
    assert metrics_response.status_code == 200
    assert (
        'orders_sold_out_total{service_name="order-service",'
        'service_version="0.1.0",service_environment="local"} 1\n'
    ) in metrics_response.text


def test_unknown_product_does_not_increment_sold_out_metric() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-metric-unavailable-001",
        },
        json={"dropId": "drop-001", "productId": "unknown-product", "quantity": 1},
    )
    metrics_response = client.get("/metrics")

    # Then
    assert response.status_code == 409
    assert "product unavailable" in response.text
    assert metrics_response.status_code == 200
    assert (
        'orders_sold_out_total{service_name="order-service",'
        'service_version="0.1.0",service_environment="local"} 0\n'
    ) in metrics_response.text
