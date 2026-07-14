from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime

import anyio
from fastapi.testclient import TestClient

from app.main import create_app
from app.messaging import handle_payment_approved_message, handle_payment_failed_message
from app.models import OrderId
from app.store import OrderStore, PaymentApplied, PaymentFailureApplied
from contracts import PaymentApprovedEvent, PaymentFailedEvent


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


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


def test_payment_failed_kafka_message_marks_order_failed_when_payload_is_valid() -> (
    None
):
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
