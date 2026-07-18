from datetime import UTC, datetime

import anyio
from contracts import PaymentApprovedEvent, RefundFailedEvent
from fastapi.testclient import TestClient

from app.main import create_app
from app.models import DropId, IdempotencyKey, ProductId, UserId
from app.store import CreateOrderCommand, OrderCreated, OrderStore


def test_customer_cancellation_is_accepted_and_idempotently_replayed() -> None:
    # Given
    store = OrderStore()
    created = anyio.run(
        store.create_order,
        CreateOrderCommand(
            user_id=UserId("00000000-0000-4000-8000-000000000001"),
            drop_id=DropId("drop-001"),
            product_id=ProductId("product-001"),
            quantity=1,
            idempotency_key=IdempotencyKey("order-cancel-001"),
        ),
    )
    assert isinstance(created, OrderCreated)
    anyio.run(
        store.apply_payment_approved,
        PaymentApprovedEvent(
            eventId="evt-approved-cancel-001",
            userId=created.order.userId,
            sourceId="payment-cancel-001",
            occurredAt=datetime(2026, 7, 15, 1, 0, tzinfo=UTC),
            producer="payment-service",
            orderId=created.order.id,
            paymentId="payment-cancel-001",
            amount=created.order.amount,
        ),
    )
    client = TestClient(create_app(store))
    headers = {
        "X-User-Id": created.order.userId,
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "cancel-001",
    }

    # When
    accepted = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers=headers,
        json={"reason": "changed my mind"},
    )
    replayed = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers=headers,
        json={"reason": "changed my mind"},
    )
    conflicting = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers=headers,
        json={"reason": "different reason"},
    )

    # Then
    assert accepted.status_code == 202
    assert replayed.status_code == 202
    assert replayed.json() == accepted.json()
    assert accepted.json()["data"] == {
        "id": accepted.json()["data"]["id"],
        "orderId": created.order.id,
        "reason": "changed my mind",
        "orderStatus": "CANCEL_PENDING",
        "refundStatus": "REQUESTED",
        "createdAt": accepted.json()["data"]["createdAt"],
        "completedAt": None,
    }
    assert conflicting.status_code == 409
    assert conflicting.json()["error"] == {
        "code": "cancellation.idempotency_conflict",
        "message": "idempotency key reused with different cancellation request",
        "details": {"orderId": created.order.id},
    }


def test_pending_payment_order_cancellation_is_rejected() -> None:
    # Given
    store = OrderStore()
    created = anyio.run(
        store.create_order,
        CreateOrderCommand(
            user_id=UserId("00000000-0000-4000-8000-000000000002"),
            drop_id=DropId("drop-001"),
            product_id=ProductId("product-001"),
            quantity=1,
            idempotency_key=IdempotencyKey("order-cancel-pending"),
        ),
    )
    assert isinstance(created, OrderCreated)
    client = TestClient(create_app(store))

    # When
    response = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "cancel-pending",
        },
        json={"reason": "too early"},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["error"]["message"] == (
        "order is not eligible for cancellation"
    )


def test_customer_can_read_failed_refund_status_after_poison_reason_is_ignored() -> (
    None
):
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "failed-status")
    client = TestClient(create_app(store))
    headers = {
        "X-User-Id": created.order.userId,
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "cancel-failed-status",
    }
    accepted = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers=headers,
        json={"reason": "customer request"},
    )
    refund_id = accepted.json()["data"]["id"]
    event = RefundFailedEvent(
        eventId="evt-refund-failed-status",
        userId=created.order.userId,
        sourceId=refund_id,
        occurredAt=datetime(2026, 7, 15, 2, 0, tzinfo=UTC),
        producer="payment-service",
        refundId=refund_id,
        orderId=created.order.id,
        paymentId="payment-failed-status",
        amount=created.order.amount,
        reason="provider terminal failure",
    )

    # When
    poison_applied = anyio.run(
        store.apply_refund_failed,
        event.model_copy(update={"reason": "provider\x00failure"}),
    )
    genuine_applied = anyio.run(store.apply_refund_failed, event)
    status_response = client.get(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert poison_applied is False
    assert genuine_applied is True
    assert status_response.status_code == 200
    assert status_response.json()["data"]["orderStatus"] == "CANCEL_PENDING"
    assert status_response.json()["data"]["refundStatus"] == "FAILED"


def test_cancellation_key_cannot_be_reused_for_another_order() -> None:
    # Given
    store = OrderStore()
    first = _confirmed_order(store, "shared-key-first")
    second = _confirmed_order(store, "shared-key-second")
    client = TestClient(create_app(store))
    headers = {
        "X-User-Id": first.order.userId,
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "cancel-shared-key",
    }
    accepted = client.post(
        f"/orders/{first.order.id}/cancellations",
        headers=headers,
        json={"reason": "same reason"},
    )

    # When
    response = client.post(
        f"/orders/{second.order.id}/cancellations",
        headers=headers,
        json={"reason": "same reason"},
    )

    # Then
    assert accepted.status_code == 202
    assert response.status_code == 409
    assert response.json()["error"]["message"] == (
        "idempotency key reused with different cancellation request"
    )


def test_cancellation_rejects_empty_idempotency_key() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "empty-key")
    client = TestClient(create_app(store))

    # When
    response = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": created.order.userId,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "",
        },
        json={"reason": "empty key must fail"},
    )

    # Then
    assert response.status_code == 422


def test_cancellation_forged_admin_role_does_not_bypass_ownership() -> None:
    # Given
    store = OrderStore()
    created = _confirmed_order(store, "forged-admin-owner")
    client = TestClient(create_app(store))

    # When
    response = client.post(
        f"/orders/{created.order.id}/cancellations",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000003",
            "X-User-Role": "ADMIN",
            "Idempotency-Key": "cancel-role-forbidden",
        },
        json={"reason": "customer request"},
    )

    # Then
    assert response.status_code == 403
    assert response.json()["error"]["message"] == "order owner mismatch"


def test_cancellation_runtime_openapi_matches_static_operation() -> None:
    # Given
    operation = (
        TestClient(create_app(OrderStore()))
        .get("/openapi.json")
        .json()["paths"]["/orders/{order_id}/cancellations"]["post"]
    )

    # When
    parameter_names = {
        parameter["name"] for parameter in operation["parameters"]
    }

    # Then
    assert operation["operationId"] == "cancelOrder"
    assert set(operation["responses"]) == {
        "202",
        "401",
        "403",
        "404",
        "409",
        "422",
        "500",
    }
    assert "X-User-Role" not in parameter_names


def _confirmed_order(store: OrderStore, suffix: str) -> OrderCreated:
    created = anyio.run(
        store.create_order,
        CreateOrderCommand(
            user_id=UserId("00000000-0000-4000-8000-000000000004"),
            drop_id=DropId("drop-001"),
            product_id=ProductId("product-001"),
            quantity=1,
            idempotency_key=IdempotencyKey(f"order-{suffix}"),
        ),
    )
    assert isinstance(created, OrderCreated)
    anyio.run(
        store.apply_payment_approved,
        PaymentApprovedEvent(
            eventId=f"evt-approved-{suffix}",
            userId=created.order.userId,
            sourceId=f"payment-{suffix}",
            occurredAt=datetime(2026, 7, 15, 1, 0, tzinfo=UTC),
            producer="payment-service",
            orderId=created.order.id,
            paymentId=f"payment-{suffix}",
            amount=created.order.amount,
        ),
    )
    return created
