from collections.abc import AsyncIterator, Sequence
from datetime import UTC, datetime

import anyio
from contracts import OrderCreatedEvent

from app.messaging import OrderCreatedConsumer, handle_order_created_message
from app.models import OrderId, UserId
from app.store import KnownOrder, PaymentStore


class FakeKafkaMessage:
    def __init__(
        self,
        *,
        topic: str,
        partition: int,
        offset: int,
        headers: Sequence[tuple[str | bytes, bytes]] | None,
        value: bytes | None,
    ) -> None:
        self.topic = topic
        self.partition = partition
        self.offset = offset
        self.headers = headers
        self.value = value


class OneMessageConsumer:
    def __init__(self, message: FakeKafkaMessage, events: list[str]) -> None:
        self._message = message
        self._events = events

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self._events.append("offset_committed")

    async def _messages(self) -> AsyncIterator[FakeKafkaMessage]:
        yield self._message

    def __aiter__(self) -> AsyncIterator[FakeKafkaMessage]:
        return self._messages()


class CommitRecordingPaymentStore(PaymentStore):
    def __init__(self, events: list[str]) -> None:
        super().__init__()
        self._events = events

    async def record_order_created(self, event: OrderCreatedEvent) -> KnownOrder:
        known_order = await super().record_order_created(event)
        self._events.append("database_committed")
        return known_order


def test_order_created_kafka_message_records_known_order_when_payload_is_valid() -> (
    None
):
    # Given
    store = PaymentStore()
    event = _order_created()
    message = FakeKafkaMessage(
        topic="order.created",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_order_created_message, message, store)

    # Then
    known_order = anyio.run(store.get_known_order, "order-001")
    assert known_order == KnownOrder(
        order_id=OrderId("order-001"),
        user_id=UserId("user-001"),
        amount=50000,
    )


def test_order_created_consumer_commits_offset_after_database_commit() -> None:
    # Given
    events: list[str] = []
    message = FakeKafkaMessage(
        topic="order.created",
        partition=0,
        offset=1,
        headers=None,
        value=_order_created().model_dump_json().encode(),
    )
    consumer = OrderCreatedConsumer(
        OneMessageConsumer(message, events),
        CommitRecordingPaymentStore(events),
    )

    # When
    anyio.run(consumer.run)

    # Then
    assert events == ["database_committed", "offset_committed"]


def test_event_id_collision_is_acked_without_in_memory_projection() -> None:
    # Given
    store = PaymentStore()
    original = _order_created()
    collision = original.model_copy(
        update={"orderId": "order-collision", "sourceId": "order-collision"}
    )
    anyio.run(store.record_order_created, original)

    # When
    anyio.run(store.record_order_created, collision)

    # Then
    assert anyio.run(store.get_known_order, "order-collision") is None


def _order_created() -> OrderCreatedEvent:
    return OrderCreatedEvent(
        eventId="evt-order-created-001",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        orderId="order-001",
        dropId="drop-001",
        productId="product-001",
        quantity=1,
        amount=50000,
        idempotencyKey="order-create-001",
    )
