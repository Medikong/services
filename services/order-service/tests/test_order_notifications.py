from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime

import anyio
import pytest
from fastapi.testclient import TestClient

from app.messaging import handle_payment_approved_message
from app.models import IdempotencyKey, Order
from app.main import create_app
from app.store import OrderStore
from contracts import PaymentApprovedEvent


class RecordingOrderEventPublisher:
    def __init__(self) -> None:
        self.created_orders: list[tuple[str, IdempotencyKey]] = []
        self.notification_orders: list[str] = []

    async def publish_order_created(
        self,
        order: Order,
        idempotency_key: IdempotencyKey,
    ) -> None:
        self.created_orders.append((order.id, idempotency_key))

    async def publish_notification_requested(self, order: Order) -> None:
        self.notification_orders.append(order.id)


class FailingOnceOrderEventPublisher(RecordingOrderEventPublisher):
    def __init__(self) -> None:
        super().__init__()
        self._should_fail = True

    async def publish_notification_requested(self, order: Order) -> None:
        if self._should_fail:
            self._should_fail = False
            msg = "temporary notification publish failure"
            raise RuntimeError(msg)
        await super().publish_notification_requested(order)


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


def test_payment_approved_message_retries_notification_request_when_first_publish_fails() -> None:
    # Given
    store = OrderStore()
    publisher = FailingOnceOrderEventPublisher()
    client = TestClient(create_app(store, publisher))
    create_response = client.post(
        "/orders",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "notification-order-001",
        },
        json={"dropId": "drop-001", "productId": "product-001", "quantity": 1},
    )
    order_id = create_response.json()["data"]["id"]
    event = PaymentApprovedEvent(
        eventId="evt-payment-approved-notification-001",
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
    with pytest.raises(RuntimeError):
        anyio.run(handle_payment_approved_message, message, store, publisher)
    anyio.run(handle_payment_approved_message, message, store, publisher)

    # Then
    assert publisher.notification_orders == [order_id]
