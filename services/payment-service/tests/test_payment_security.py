from datetime import UTC, datetime
from typing import Final

import anyio
from fastapi.testclient import TestClient

from app.main import create_app
from app.models import Payment
from app.store import PaymentStore
from contracts import OrderCreatedEvent

DEFAULT_ORDER_CREATED: Final = OrderCreatedEvent(
    eventId="evt-order-created-security",
    userId="user-001",
    sourceId="order-001",
    occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
    producer="order-service",
    orderId="order-001",
    dropId="drop-001",
    productId="product-001",
    quantity=1,
    amount=50000,
    idempotencyKey="order-create-security",
)


class RecordingPaymentEventPublisher:
    def __init__(self) -> None:
        self.published_payments: list[Payment] = []
        self.published_failed_payments: list[Payment] = []

    async def publish_payment_approved(self, payment: Payment) -> None:
        self.published_payments.append(payment)

    async def publish_payment_failed(self, payment: Payment) -> None:
        self.published_failed_payments.append(payment)


def record_known_order(store: PaymentStore) -> None:
    anyio.run(store.record_order_created, DEFAULT_ORDER_CREATED)


def test_approve_mock_payment_returns_409_when_order_is_unknown() -> None:
    # Given
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(PaymentStore(), publisher))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-unknown-order-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "order is not ready for payment"
    assert publisher.published_payments == []


def test_fail_mock_payment_returns_409_when_order_is_unknown() -> None:
    # Given
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(PaymentStore(), publisher))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-fail-unknown-order-001",
        },
        json={
            "orderId": "order-001",
            "amount": 50000,
            "method": "MOCK_CARD",
            "reason": "card_declined",
        },
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "order is not ready for payment"
    assert publisher.published_failed_payments == []


def test_approve_mock_payment_returns_409_when_amount_mismatches_order() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-amount-mismatch-001",
        },
        json={"orderId": "order-001", "amount": 60000, "method": "MOCK_CARD"},
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "payment request does not match order"
    assert publisher.published_payments == []


def test_fail_mock_payment_returns_409_when_amount_mismatches_order() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-fail-amount-mismatch-001",
        },
        json={
            "orderId": "order-001",
            "amount": 60000,
            "method": "MOCK_CARD",
            "reason": "card_declined",
        },
    )

    # Then
    assert response.status_code == 409
    assert response.json()["detail"] == "payment request does not match order"
    assert publisher.published_failed_payments == []


def test_approve_mock_payment_returns_409_when_idempotency_payload_changes() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))
    headers = {
        "X-User-Id": "user-001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "payment-idempotency-conflict-001",
    }

    # When
    first_response = client.post(
        "/payments/mock-approvals",
        headers=headers,
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )
    second_response = client.post(
        "/payments/mock-approvals",
        headers=headers,
        json={"orderId": "order-001", "amount": 60000, "method": "MOCK_CARD"},
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409
    assert second_response.json()["detail"] == "idempotency key reused with different payment request"
    assert len(publisher.published_payments) == 1


def test_fail_mock_payment_returns_409_when_idempotency_payload_changes() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))
    headers = {
        "X-User-Id": "user-001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "payment-fail-idempotency-conflict-001",
    }

    # When
    first_response = client.post(
        "/payments/mock-failures",
        headers=headers,
        json={
            "orderId": "order-001",
            "amount": 50000,
            "method": "MOCK_CARD",
            "reason": "card_declined",
        },
    )
    second_response = client.post(
        "/payments/mock-failures",
        headers=headers,
        json={
            "orderId": "order-001",
            "amount": 50000,
            "method": "MOCK_CARD",
            "reason": "limit_exceeded",
        },
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 409
    assert second_response.json()["detail"] == "idempotency key reused with different payment request"
    assert len(publisher.published_failed_payments) == 1


def test_approve_mock_payment_returns_422_when_order_id_is_empty() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-empty-order-001",
        },
        json={"orderId": "", "amount": 50000, "method": "MOCK_CARD"},
    )

    # Then
    assert response.status_code == 422
