from datetime import UTC, datetime

import anyio
from fastapi.testclient import TestClient

from app.catalog import ProductForSale
from app.cancellations import (
    CancellationAlreadyRequested,
    CancellationRequested,
    RequestCancellationCommand,
)
from app.main import create_app
from app.models import DropId, IdempotencyKey, OrderId, ProductId
from app.store import InventorySeed, OrderStore, PaymentFailureApplied
from contracts import PaymentApprovedEvent, PaymentFailedEvent, RefundCompletedEvent


def test_payment_failed_releases_reserved_stock_for_next_order() -> None:
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
    store = OrderStore(catalog, inventory)
    client = TestClient(create_app(store))
    first_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-release-001",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )
    order_id = first_response.json()["data"]["id"]
    payment_failed = PaymentFailedEvent(
        eventId="evt-payment-failed-release-001",
        userId="00000000-0000-4000-8000-000000000001",
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
            "X-User-Id": "00000000-0000-4000-8000-000000000002",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-limited-release-002",
        },
        json={"dropId": "drop-limited", "productId": "product-limited", "quantity": 1},
    )

    # Then
    assert first_response.status_code == 201
    assert isinstance(failure_result, PaymentFailureApplied)
    assert second_response.status_code == 201
    second_order = anyio.run(
        store.get_order, OrderId(second_response.json()["data"]["id"])
    )
    assert second_order is not None
    assert second_order.userId == "00000000-0000-4000-8000-000000000002"


def test_refund_completed_releases_stock_for_next_in_memory_order() -> None:
    # Given
    catalog = (
        ProductForSale(
            drop_id=DropId("drop-refund-limited"),
            product_id=ProductId("product-refund-limited"),
            unit_price=50000,
        ),
    )
    inventory = (
        InventorySeed(
            DropId("drop-refund-limited"),
            ProductId("product-refund-limited"),
            1,
        ),
    )
    store = OrderStore(catalog, inventory)
    client = TestClient(create_app(store))
    first = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000003",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-refund-limited-001",
        },
        json={
            "dropId": "drop-refund-limited",
            "productId": "product-refund-limited",
            "quantity": 1,
        },
    )
    first_order = anyio.run(store.get_order, OrderId(first.json()["data"]["id"]))
    assert first_order is not None
    anyio.run(
        store.apply_payment_approved,
        PaymentApprovedEvent(
            eventId="evt-approved-refund-limited",
            userId=first_order.userId,
            sourceId="payment-refund-limited",
            occurredAt=datetime(2026, 7, 15, 4, 0, tzinfo=UTC),
            producer="payment-service",
            orderId=first_order.id,
            paymentId="payment-refund-limited",
            amount=first_order.amount,
        ),
    )
    cancellation = anyio.run(
        store.request_cancellation,
        RequestCancellationCommand(
            order_id=first_order.id,
            user_id=first_order.userId,
            idempotency_key=IdempotencyKey("cancel-refund-limited"),
            reason="customer request",
        ),
    )
    assert isinstance(cancellation, CancellationRequested)

    # When
    refund_completed = RefundCompletedEvent(
        eventId="evt-refund-completed-limited",
        userId=first_order.userId,
        sourceId=cancellation.cancellation.id,
        occurredAt=datetime(2026, 7, 15, 4, 1, tzinfo=UTC),
        producer="payment-service",
        refundId=cancellation.cancellation.id,
        orderId=first_order.id,
        paymentId="payment-refund-limited",
        amount=first_order.amount,
    )
    invalid_amount = anyio.run(
        store.apply_refund_completed,
        refund_completed.model_copy(update={"amount": first_order.amount + 1}),
    )
    invalid_producer = anyio.run(
        store.apply_refund_completed,
        refund_completed.model_copy(update={"producer": "forged-service"}),
    )
    invalid_timezone = anyio.run(
        store.apply_refund_completed,
        refund_completed.model_copy(
            update={"occurredAt": datetime(2026, 7, 15, 4, 1)},
        ),
    )
    completed = anyio.run(
        store.apply_refund_completed,
        refund_completed,
    )
    replayed_cancellation = anyio.run(
        store.request_cancellation,
        RequestCancellationCommand(
            order_id=first_order.id,
            user_id=first_order.userId,
            idempotency_key=IdempotencyKey("cancel-refund-limited"),
            reason="customer request",
        ),
    )
    second = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000004",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "order-refund-limited-002",
        },
        json={
            "dropId": "drop-refund-limited",
            "productId": "product-refund-limited",
            "quantity": 1,
        },
    )

    # Then
    assert (invalid_amount, invalid_producer, invalid_timezone) == (False, False, False)
    assert completed is True
    assert isinstance(replayed_cancellation, CancellationAlreadyRequested)
    assert replayed_cancellation.cancellation == cancellation.cancellation
    assert second.status_code == 201
