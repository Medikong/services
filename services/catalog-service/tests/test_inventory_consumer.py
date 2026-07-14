from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from datetime import UTC, datetime

import anyio
import pytest
from aiokafka.structs import ConsumerRecord
from contracts import InventoryChangedEvent

from app.messaging import (
    InventoryChangedConsumer,
    KafkaMessage,
    inventory_consumer_factory,
)


@dataclass(slots=True)  # noqa: MUTABLE_OK
class Consumer:
    messages: tuple[KafkaMessage, ...]
    commits: int = 0

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self.commits += 1

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        for message in self.messages:
            yield message

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._messages()


class CommitObservedError(Exception):
    """Signal that a live consumer committed before stream exhaustion."""


@dataclass(slots=True)  # noqa: MUTABLE_OK
class NonExhaustingConsumer(Consumer):
    async def commit(self) -> None:
        raise CommitObservedError

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        yield self.messages[0]
        await anyio.sleep_forever()


@dataclass(slots=True)  # noqa: MUTABLE_OK
class Repository:
    events: list[InventoryChangedEvent] = field(default_factory=list)
    fails: bool = False

    async def apply_inventory_changed(self, event: InventoryChangedEvent) -> None:
        if self.fails:
            raise RuntimeError
        self.events.append(event)


def _message() -> KafkaMessage:
    event = InventoryChangedEvent(
        eventId="evt-inventory-v1",
        userId="system",
        sourceId="inventory:product-001",
        occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
        producer="order-service",
        correlationId="snapshot-001",
        dropId="drop-001",
        productId="product-001",
        totalQuantity=42,
        reservedQuantity=10,
        soldQuantity=0,
        remainingQuantity=32,
        inventoryVersion=1,
    )
    return ConsumerRecord(
        topic="inventory.changed",
        partition=0,
        offset=7,
        timestamp=0,
        timestamp_type=0,
        key=None,
        value=event.model_dump_json().encode(),
        checksum=None,
        serialized_key_size=-1,
        serialized_value_size=-1,
        headers=(),
    )


def test_consumer_commits_only_after_projection_is_persisted() -> None:
    # Given
    client = Consumer(messages=(_message(),))
    repository = Repository()
    consumer = InventoryChangedConsumer(client, repository)

    # When
    anyio.run(consumer.run)

    # Then
    assert [event.eventId for event in repository.events] == ["evt-inventory-v1"]
    assert client.commits == 1


def test_consumer_leaves_offset_uncommitted_when_projection_fails() -> None:
    # Given
    client = Consumer(messages=(_message(),))
    repository = Repository(fails=True)
    consumer = InventoryChangedConsumer(client, repository)

    # When / Then
    with pytest.raises(RuntimeError):
        anyio.run(consumer.run)
    assert client.commits == 0


def test_consumer_commits_before_a_live_stream_exhausts() -> None:
    # Given
    client = NonExhaustingConsumer(messages=(_message(),))
    repository = Repository()
    consumer = InventoryChangedConsumer(client, repository)

    # When / Then
    with pytest.raises(CommitObservedError):
        anyio.run(consumer.run)
    assert [event.eventId for event in repository.events] == ["evt-inventory-v1"]


def test_kafka_client_construction_is_deferred_until_lifespan() -> None:
    # Given / When
    factory = inventory_consumer_factory(Repository(), "kafka:9092")

    # Then
    assert callable(factory)
