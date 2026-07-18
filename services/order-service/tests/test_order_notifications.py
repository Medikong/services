from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from inspect import getsource

import anyio
from fastapi.testclient import TestClient

from app.messaging import handle_payment_approved_message
from app.models import OrderId
from app.main import create_app
from app.store import OrderStore
from contracts import PaymentApprovedEvent


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


def test_payment_approved_message_applies_domain_change_without_direct_publish() -> (
    None
):
    # Given
    store = OrderStore()
    client = TestClient(create_app(store))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "notification-order-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentApprovedEvent(
        eventId="evt-payment-approved-notification-001",
        userId="00000000-0000-4000-8000-000000000001",
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
    order = anyio.run(store.get_order, OrderId(order_id))
    assert order is not None
    assert order.status == "CONFIRMED"


def test_http_and_consumer_paths_have_no_direct_publish_call() -> None:
    # Given
    source = "\n".join(
        (getsource(create_app), getsource(handle_payment_approved_message))
    )

    # When
    direct_publish_is_present = ".publish_" in source

    # Then
    assert direct_publish_is_present is False
