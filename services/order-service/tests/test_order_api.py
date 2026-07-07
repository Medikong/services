from datetime import UTC, datetime
from dataclasses import dataclass
from collections.abc import Sequence

import anyio
import pytest
from fastapi.testclient import TestClient

from app.catalog import ProductForSale
from app.db import resources_from_env
from app.main import create_app
from app.messaging import (
    NoopOrderEventPublisher,
    handle_payment_approved_message,
    handle_payment_failed_message,
)
from app.models import DropId, IdempotencyKey, Order, OrderId, ProductId
from app.store import OrderStore, PaymentApplied, PaymentFailureApplied
from contracts import PaymentApprovedEvent, PaymentFailedEvent


class RecordingOrderEventPublisher:
    def __init__(self) -> None:
        self.published_orders: list[tuple[str, IdempotencyKey]] = []

    async def publish_order_created(
        self,
        order: Order,
        idempotency_key: IdempotencyKey,
    ) -> None:
        self.published_orders.append((order.id, idempotency_key))


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


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
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert 'service_ready{service_name="order-service"' in response.text


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
    assert isinstance(resources.event_publisher, NoopOrderEventPublisher)
    assert resources.kafka_bootstrap_servers == "kafka:9092"
    assert resources.kafka_runtime is None


def test_create_order_returns_pending_order_when_customer_requests_known_product() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-create-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["userId"] == "user-001"
    assert body["data"]["dropId"] == "drop-001"
    assert body["data"]["productId"] == "product-001"
    assert body["data"]["quantity"] == 1
    assert body["data"]["amount"] == 50000
    assert body["data"]["status"] == "PENDING_PAYMENT"


def test_create_order_reuses_order_when_idempotency_key_repeats() -> None:
    # Given
    publisher = RecordingOrderEventPublisher()
    client = TestClient(create_app(OrderStore(), publisher))
    headers = {
        "X-User-Id": "user-001",
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
    assert publisher.published_orders == [
        (first_response.json()["data"]["id"], IdempotencyKey("order-create-replay-001")),
    ]


def test_create_order_returns_409_when_idempotency_key_payload_changes() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))
    headers = {
        "X-User-Id": "user-001",
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
    assert second_response.json()["detail"] == "idempotency key reused with different order request"


def test_get_order_returns_403_when_customer_reads_another_customer_order() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-owner-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/orders/{order_id}",
        headers={"X-User-Id": "user-002", "X-User-Role": "CUSTOMER"},
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
            "X-User-Id": "user-001",
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
            remaining_quantity=1,
        ),
    )
    client = TestClient(create_app(OrderStore(catalog)))

    # When
    first_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-001",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )
    second_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-002",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-002",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409


def test_create_order_returns_403_when_owner_role_requests_order_creation() -> None:
    # Given
    client = TestClient(create_app(OrderStore()))

    # When
    response = client.post(
        "/orders",
        headers={
            "X-User-Id": "owner-001",
            "X-User-Role": "OWNER",
            "Idempotency-Key": "owner-order-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )

    # Then
    assert response.status_code == 403


def test_apply_payment_approved_confirms_order_when_payment_event_matches() -> None:
    # Given
    store = OrderStore()
    client = TestClient(create_app(store))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-approved-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentApprovedEvent(
        eventId="evt-payment-approved-001",
        userId="user-001",
        sourceId=order_id,
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId=order_id,
        paymentId="payment-001",
        amount=50000,
    )

    # When
    result = anyio.run(store.apply_payment_approved, event)

    # Then
    assert isinstance(result, PaymentApplied)
    assert result.order.status == "CONFIRMED"
    assert result.order.paymentId == "payment-001"
    assert result.order.confirmedAt == datetime(2026, 7, 3, 12, 0, tzinfo=UTC)


def test_payment_approved_kafka_message_confirms_order_when_payload_is_valid() -> None:
    # Given
    store = OrderStore()
    client = TestClient(create_app(store))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-approved-kafka-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentApprovedEvent(
        eventId="evt-payment-approved-kafka-001",
        userId="user-001",
        sourceId=order_id,
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId=order_id,
        paymentId="payment-001",
        amount=50000,
    )
    message = FakeKafkaMessage(
        topic="payment.approved",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_payment_approved_message, message, store)

    # Then
    confirmed_order = anyio.run(store.get_order, OrderId(order_id))
    assert confirmed_order is not None
    assert confirmed_order.status == "CONFIRMED"
    assert confirmed_order.paymentId == "payment-001"


def test_apply_payment_failed_marks_order_failed_when_payment_event_matches() -> None:
    # Given
    store = OrderStore()
    client = TestClient(create_app(store))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-failed-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentFailedEvent(
        eventId="evt-payment-failed-001",
        userId="user-001",
        sourceId=order_id,
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId=order_id,
        paymentId="payment-001",
        amount=50000,
        reason="card_declined",
    )

    # When
    result = anyio.run(store.apply_payment_failed, event)

    # Then
    assert isinstance(result, PaymentFailureApplied)
    assert result.order.status == "PAYMENT_FAILED"
    assert result.order.paymentId == "payment-001"
    assert result.order.confirmedAt is None


def test_payment_failed_kafka_message_marks_order_failed_when_payload_is_valid() -> None:
    # Given
    store = OrderStore()
    client = TestClient(create_app(store))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-failed-kafka-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentFailedEvent(
        eventId="evt-payment-failed-kafka-001",
        userId="user-001",
        sourceId=order_id,
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId=order_id,
        paymentId="payment-001",
        amount=50000,
        reason="card_declined",
    )
    message = FakeKafkaMessage(
        topic="payment.failed",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_payment_failed_message, message, store)

    # Then
    failed_order = anyio.run(store.get_order, OrderId(order_id))
    assert failed_order is not None
    assert failed_order.status == "PAYMENT_FAILED"
    assert failed_order.paymentId == "payment-001"
