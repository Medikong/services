import pytest
from fastapi.testclient import TestClient

from app.catalog import ProductForSale
from app.db import resources_from_env
from app.main import create_app
from app.models import DropId, ProductId
from app.store import InventorySeed, OrderStore


def test_healthz_returns_order_service_identity() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.get("/healthz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "order-service"


def test_readyz_returns_ready_order_checks() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.get("/readyz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ready"
    assert response.json()["checks"] == {
        "orders": "ok",
        "payment_approved_handler": "ok",
        "payment_failed_handler": "ok",
    }


def test_metrics_exposes_order_service_readiness_metric() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    client.get("/healthz")
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert 'service_name="order-service"' in response.text
    assert _service_ready_value(response.text) == 0.0
    assert "# TYPE http_server_request_duration_seconds histogram" in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_route_kind="probe"' in response.text
    assert 'http_response_status_code="200"' in response.text

    assert client.get("/readyz").status_code == 200
    assert _service_ready_value(client.get("/metrics").text) == 1.0


def _service_ready_value(metrics_text: str) -> float:
    line = next(
        line for line in metrics_text.splitlines() if line.startswith("service_ready{")
    )
    return float(line.rsplit(" ", maxsplit=1)[1])


def test_resources_from_env_defers_kafka_clients_until_lifespan(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")

    # When
    resources = resources_from_env()

    # Then
    assert isinstance(resources.repository, OrderStore)
    assert resources.kafka_bootstrap_servers == "kafka:9092"
    assert resources.kafka_runtime is None


def test_create_order_returns_pending_order_when_customer_requests_known_product() -> (
    None
):
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-create-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["userId"] == "00000000-0000-4000-8000-000000000001"
    assert body["data"]["dropId"] == "drop-001"
    assert body["data"]["productId"] == "product-001"
    assert body["data"]["quantity"] == 1
    assert body["data"]["amount"] == 50000
    assert body["data"]["status"] == "PENDING_PAYMENT"


def test_create_order_reuses_order_when_idempotency_key_repeats() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))
    headers = {
        "X-User-Id": "00000000-0000-4000-8000-000000000001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "order-create-replay-001",
    }
    payload = {"dropId": "drop-001", "productId": "product-001", "quantity": 1}

    # When
    first_response = client.post("/orders", headers=headers, json=payload)
    second_response = client.post("/orders", headers=headers, json=payload)

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 201
    assert first_response.json()["data"]["id"] == second_response.json()["data"]["id"]


def test_create_order_returns_409_when_idempotency_key_payload_changes() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))
    headers = {
        "X-User-Id": "00000000-0000-4000-8000-000000000001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "order-create-conflict-001",
    }

    # When
    first_response = client.post(
        "/orders",
        headers=headers,
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    second_response = client.post(
        "/orders",
        headers=headers,
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 2},
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409
    assert second_response.json()["error"]["message"] == (
        "idempotency key reused with different order request"
    )


def test_get_order_returns_403_when_customer_reads_another_customer_order() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-owner-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/orders/{order_id}",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000002",
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert response.status_code == 403


def test_create_order_returns_409_when_product_is_unavailable() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-unavailable-001",
        },
        json={"dropId": "drop-001", "productId": "unknown-product", "quantity": 1},
    )

    # Then
    assert response.status_code == 409


def test_create_order_returns_409_when_reserved_stock_is_sold_out() -> None:
    # Given
    catalog = (
        ProductForSale(
            drop_id=DropId("drop-limited"),
            product_id=ProductId("product-limited"),
            unit_price=50000,
        ),
    )
    inventory = (
        InventorySeed(DropId("drop-limited"), ProductId("product-limited"), 1),
    )
    client = TestClient(create_app(OrderStore(catalog, inventory)))

    # When
    first_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-001",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )
    second_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000002",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-002",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409


def test_create_order_ignores_untrusted_owner_role_header() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000003",
            "X-User-Role": "OWNER",
            "Idempotency-Key": "owner-order-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["userId"] == ("00000000-0000-4000-8000-000000000003")
