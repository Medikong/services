from datetime import UTC, datetime

import anyio
from fastapi.testclient import TestClient

from app.catalog import ProductForSale
from app.main import create_app
from app.models import DropId, OrderId, ProductId
from app.store import OrderStore, PaymentFailureApplied
from contracts import PaymentFailedEvent


def test_payment_failed_releases_reserved_stock_for_next_order() -> None:
    # Given
    catalog = (
        ProductForSale(
            drop_id=DropId("drop-limited"),
            product_id=ProductId("product-limited"),
            unit_price=50000,
            remaining_quantity=1,
        ),
    )
    store = OrderStore(catalog)
    client = TestClient(create_app(store))
    first_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-release-001",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )
    order_id = first_response.json()["data"]["id"]
    payment_failed = PaymentFailedEvent(
        eventId="evt-payment-failed-release-001",
        userId="user-001",
        sourceId=order_id,
        occurredAt=datetime(2026, 7, 7, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId=order_id,
        paymentId="payment-failed-release-001",
        amount=50000,
        reason="card_declined",
    )

    # When
    failure_result = anyio.run(store.apply_payment_failed, payment_failed)
    second_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-002",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-release-002",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )

    # Then
    assert first_response.status_code == 201
    assert isinstance(failure_result, PaymentFailureApplied)
    assert second_response.status_code == 201
    second_order = anyio.run(store.get_order, OrderId(second_response.json()["data"]["id"]))
    assert second_order is not None
    assert second_order.userId == "user-002"
