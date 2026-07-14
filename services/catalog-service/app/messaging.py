"""Inventory projection Kafka boundary."""

from collections.abc import AsyncIterator, Callable, Sequence
from dataclasses import dataclass
from typing import Protocol, final

from aiokafka import AIOKafkaConsumer
from aiokafka.structs import ConsumerRecord
from contracts import INVENTORY_CHANGED_TOPIC, InventoryChangedEvent
from kafka_utils import start_consumer_span
from pydantic import ValidationError


class InventoryProjectionRepository(Protocol):
    """Persistence operation required by the inventory consumer."""

    async def apply_inventory_changed(self, event: InventoryChangedEvent) -> None:
        """Atomically record and apply an inventory event."""
        ...


type KafkaMessage = ConsumerRecord[bytes, bytes]


@dataclass(slots=True)  # noqa: MUTABLE_OK
class _TraceMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None


class KafkaConsumerClient(Protocol):
    """Minimal manually committed Kafka consumer contract."""

    async def start(self) -> None:
        """Start the consumer."""
        ...

    async def stop(self) -> None:
        """Stop the consumer."""
        ...

    async def commit(self) -> None:
        """Commit the current offset."""
        ...

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        """Iterate consumed messages."""
        ...


@final
class InventoryChangedConsumer:
    """Consume inventory events and commit only after persistence succeeds."""

    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: InventoryProjectionRepository,
    ) -> None:
        """Store consumer and projection dependencies."""
        self._consumer: KafkaConsumerClient = consumer
        self._repository: InventoryProjectionRepository = repository

    async def start(self) -> None:
        """Start consuming inventory events."""
        await self._consumer.start()

    async def stop(self) -> None:
        """Stop consuming inventory events."""
        await self._consumer.stop()

    async def run(self) -> None:
        """Process messages until cancellation or consumer exhaustion."""
        async for message in self._consumer:
            await handle_inventory_changed_message(message, self._repository)
            await self._consumer.commit()


type InventoryConsumerFactory = Callable[[], InventoryChangedConsumer]


async def handle_inventory_changed_message(
    message: KafkaMessage,
    repository: InventoryProjectionRepository,
) -> None:
    """Parse and persist one valid inventory event."""
    if message.value is None or message.topic != INVENTORY_CHANGED_TOPIC:
        return
    with start_consumer_span(
        _TraceMessage(
            topic=message.topic,
            partition=message.partition,
            offset=message.offset,
            headers=tuple(message.headers),
        ),
        service_name="catalog-service",
        name="kafka.consume inventory.changed",
    ):
        try:
            event = InventoryChangedEvent.model_validate_json(message.value)
        except ValidationError:
            return
        await repository.apply_inventory_changed(event)


def inventory_consumer_factory(
    repository: InventoryProjectionRepository,
    bootstrap_servers: str,
) -> InventoryConsumerFactory | None:
    """Defer manual-commit client construction until lifespan startup."""
    if bootstrap_servers == "":
        return None

    def factory() -> InventoryChangedConsumer:
        consumer = AIOKafkaConsumer(
            INVENTORY_CHANGED_TOPIC,
            bootstrap_servers=bootstrap_servers,
            group_id="catalog-service-inventory-projection",
            auto_offset_reset="earliest",
            enable_auto_commit=False,
        )
        return InventoryChangedConsumer(consumer, repository)

    return factory
