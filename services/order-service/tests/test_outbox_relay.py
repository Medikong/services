from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import cast

import anyio
from contracts import PaymentFailedEvent
from kafka_utils import KafkaProducerOption, TraceAwareKafkaProducer
import pytest

from app.messaging import KafkaMessage, KafkaOutboxPublisher, PaymentConsumer
from app.models import OrderId
from app.outbox import MAX_RETRY_DELAY, OutboxMessage, RelayPayload, retry_delay
from app.store import PaymentEventOrderMissing


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


class OneMessageConsumer:
    def __init__(self, message: KafkaMessage, events: list[str]) -> None:
        self._message = message
        self._events = events

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self._events.append("offset_committed")

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        yield self._message

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._messages()


class CommitRecordingRepository:
    def __init__(self, events: list[str]) -> None:
        self._events = events

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentEventOrderMissing:
        self._events.append("database_committed")
        return PaymentEventOrderMissing(order_id=OrderId(event.orderId))


class RecordingProducer:
    def __init__(self) -> None:
        self.topic: str | None = None
        self.value: RelayPayload | None = None
        self.key: bytes | None = None

    async def send_and_wait(
        self,
        topic: str,
        value: RelayPayload,
        *options: KafkaProducerOption,
        key: bytes,
    ) -> None:
        self.topic = topic
        self.value = value
        self.key = key


def test_retry_delay_uses_bounded_exponential_backoff() -> None:
    # Given
    attempts = (1, 2, 9, 10)

    # When
    delays = tuple(retry_delay(attempt) for attempt in attempts)

    # Then
    assert delays == (
        timedelta(seconds=1),
        timedelta(seconds=2),
        timedelta(seconds=256),
        MAX_RETRY_DELAY,
    )


def test_kafka_publisher_uses_stored_correlation_id_and_order_key(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    producer = RecordingProducer()
    captured_correlation_ids: list[str] = []
    monkeypatch.setattr(
        "app.messaging.with_correlation_id",
        lambda value: captured_correlation_ids.append(value),
    )
    publisher = KafkaOutboxPublisher(cast(TraceAwareKafkaProducer, producer))
    message = OutboxMessage(
        event_id="event-001",
        topic="order.created",
        message_key="order-001",
        payload={"eventId": "event-001", "correlationId": "request-001"},
    )

    # When
    anyio.run(publisher.publish, message)

    # Then
    assert captured_correlation_ids == ["request-001"]
    assert producer.topic == "order.created"
    assert producer.value == message.payload
    assert producer.key == b"order-001"


def test_payment_consumer_commits_offset_only_after_database_commit() -> None:
    # Given
    events: list[str] = []
    event = PaymentFailedEvent(
        eventId="evt-offset-ordering-001",
        userId="user-001",
        sourceId="payment-001",
        occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
        producer="payment-service",
        orderId="order-001",
        paymentId="payment-001",
        amount=50000,
        reason="card_declined",
    )
    message = FakeKafkaMessage(
        topic="payment.failed",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode(),
    )
    consumer = PaymentConsumer(
        OneMessageConsumer(message, events),
        CommitRecordingRepository(events),
    )

    # When
    anyio.run(consumer.run)

    # Then
    assert events == ["database_committed", "offset_committed"]
