from datetime import UTC, datetime
from typing import Final

import anyio
from fastapi.testclient import TestClient

from app.main import create_app
from app.store import PaymentStore
from contracts import OrderCreatedEvent

DEFAULT_ORDER_CREATED: Final = OrderCreatedEvent(
    eventId="evt-order-created-metric",
    userId="user-001",
    sourceId="order-001",
    occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
    producer="order-service",
    orderId="order-001",
    dropId="drop-001",
    productId="product-001",
    quantity=1,
    amount=50000,
    idempotencyKey="order-create-metric",
)


def test_approve_mock_payment_increments_approved_metric() -> None:
    # Given
    store = PaymentStore()
    anyio.run(store.record_order_created, DEFAULT_ORDER_CREATED)
    client = TestClient(create_app(store))

    # When
    payment_response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-metric-approved-001",
        },
        json={"orderId": "order-001", "amount": 50000},
    )
    metrics_response = client.get("/metrics")

    # Then
    assert payment_response.status_code == 201
    assert metrics_response.status_code == 200
    assert (
        'payments_approved_total{service_name="payment-service",'
        'service_version="0.1.0",service_environment="local"} 1\n'
    ) in metrics_response.text


def test_fail_mock_payment_increments_failed_metric() -> None:
    # Given
    store = PaymentStore()
    anyio.run(store.record_order_created, DEFAULT_ORDER_CREATED)
    client = TestClient(create_app(store))

    # When
    payment_response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-metric-failed-001",
        },
        json={"orderId": "order-001", "amount": 50000, "reason": "card_declined"},
    )
    metrics_response = client.get("/metrics")

    # Then
    assert payment_response.status_code == 201
    assert metrics_response.status_code == 200
    assert (
        'payments_failed_total{service_name="payment-service",'
        'service_version="0.1.0",service_environment="local"} 1\n'
    ) in metrics_response.text
