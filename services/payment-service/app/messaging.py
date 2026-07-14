import logging
from collections.abc import AsyncIterator, Callable, Sequence
from dataclasses import dataclass
from typing import Final, Protocol

from aiokafka import AIOKafkaConsumer
from contracts import ORDER_CREATED_TOPIC, OrderCreatedEvent
from kafka_utils import (
    KafkaProducerOption,
    TraceAwareKafkaProducer,
    create_kafka_producer,
    start_consumer_span,
    with_correlation_id,
    with_trace_context,
)
from pydantic import ValidationError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.metrics import PaymentMetrics
from app.outbox import OutboxMessage, OutboxRelay, RelayPayload
from app.repository import PaymentRepository

LOGGER: Final = logging.getLogger(__name__)


class OutboxCorrelationError(RuntimeError):
    """Raised when a persisted outbox payload lacks correlation metadata."""

    def __init__(self, event_id: str) -> None:
        super().__init__(f"outbox event {event_id} is missing correlationId")


class KafkaRuntimeConfigurationError(RuntimeError):
    """Raised when a configured Kafka worker cannot construct its client."""


class KafkaProducerClient(Protocol):
    async def start(self) -> None: ...

    async def stop(self) -> None: ...

    async def send_and_wait(
        self,
        topic: str,
        value: RelayPayload,
        *producer_options: KafkaProducerOption,
        key: bytes,
    ) -> None: ...


class KafkaOutboxPublisher:
    def __init__(
        self,
        producer: KafkaProducerClient | TraceAwareKafkaProducer,
    ) -> None:
        self._producer = producer

    async def start(self) -> None:
        await self._producer.start()

    async def stop(self) -> None:
        await self._producer.stop()

    async def publish(self, message: OutboxMessage) -> None:
        correlation_id = message.payload.get("correlationId")
        if not isinstance(correlation_id, str):
            raise OutboxCorrelationError(message.event_id)
        await self._producer.send_and_wait(
            message.topic,
            message.payload,
            with_correlation_id(correlation_id),
            with_trace_context(message.trace_context),
            key=message.message_key.encode(),
        )


class KafkaMessage(Protocol):
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


class KafkaConsumerClient(Protocol):
    async def start(self) -> None: ...

    async def stop(self) -> None: ...

    async def commit(self) -> None: ...

    def __aiter__(self) -> AsyncIterator[KafkaMessage]: ...


class _ConsumedKafkaMessage:
    """Mutable SDK-shaped message used at the aiokafka boundary."""

    __slots__ = ("headers", "offset", "partition", "topic", "value")

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


class AIOKafkaConsumerClient:
    def __init__(self, consumer: AIOKafkaConsumer) -> None:
        self._consumer = consumer

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def commit(self) -> None:
        await self._consumer.commit()

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        async for message in self._consumer:
            yield _ConsumedKafkaMessage(
                topic=message.topic,
                partition=message.partition,
                offset=message.offset,
                headers=message.headers,
                value=message.value,
            )

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._messages()


@dataclass(frozen=True, slots=True)
class KafkaRuntime:
    order_created_consumer_factory: "OrderCreatedConsumerFactory | None"
    outbox_worker_factory: "OutboxWorkerFactory | None"


@dataclass(frozen=True, slots=True)
class KafkaRuntimeConfig:
    bootstrap_servers: str
    repository: PaymentRepository
    session_factory: async_sessionmaker[AsyncSession] | None
    metrics: PaymentMetrics


class OrderCreatedConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: PaymentRepository,
    ) -> None:
        self._consumer = consumer
        self._repository = repository

    async def stop(self) -> None:
        await self._consumer.stop()

    async def start(self) -> None:
        await self._consumer.start()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_order_created_message(message, self._repository)
            await self._consumer.commit()


class OutboxWorkerRelay(Protocol):
    async def run(self) -> None: ...


type OrderCreatedConsumerFactory = Callable[[], OrderCreatedConsumer]
type OutboxWorkerFactory = Callable[[], tuple[KafkaOutboxPublisher, OutboxWorkerRelay]]


async def handle_order_created_message(
    message: KafkaMessage,
    repository: PaymentRepository,
) -> None:
    value = message.value
    if value is None:
        return
    try:
        event = OrderCreatedEvent.model_validate_json(value)
    except ValidationError:
        LOGGER.warning(
            "discarding invalid order.created payload partition=%d offset=%d",
            message.partition,
            message.offset,
        )
        return
    with start_consumer_span(
        message,
        service_name="payment-service",
        name="kafka.consume order.created",
    ):
        await repository.record_order_created(event)


def kafka_runtime_from_config(config: KafkaRuntimeConfig) -> KafkaRuntime:
    session_factory = config.session_factory
    if config.bootstrap_servers == "" or session_factory is None:
        return KafkaRuntime(None, None)

    def order_created_consumer_factory() -> OrderCreatedConsumer:
        consumer = AIOKafkaConsumer(
            ORDER_CREATED_TOPIC,
            bootstrap_servers=config.bootstrap_servers,
            group_id="payment-service-order-created",
            auto_offset_reset="earliest",
            enable_auto_commit=False,
        )
        return OrderCreatedConsumer(
            AIOKafkaConsumerClient(consumer),
            config.repository,
        )

    def outbox_worker_factory() -> tuple[KafkaOutboxPublisher, OutboxRelay]:
        producer = create_kafka_producer(
            config.bootstrap_servers,
            client_id="payment-service",
        )
        if producer is None:
            raise KafkaRuntimeConfigurationError(
                "Kafka producer requires bootstrap servers"
            )
        publisher = KafkaOutboxPublisher(producer)
        return (
            publisher,
            OutboxRelay(session_factory, publisher, config.metrics),
        )

    return KafkaRuntime(
        order_created_consumer_factory=order_created_consumer_factory,
        outbox_worker_factory=outbox_worker_factory,
    )
