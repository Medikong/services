from collections.abc import AsyncIterator, Callable, Sequence
from dataclasses import dataclass
import logging
from typing import Final, Protocol, assert_never

from aiokafka import AIOKafkaConsumer
from contracts import (
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    REFUND_COMPLETED_TOPIC,
    REFUND_FAILED_TOPIC,
    PaymentApprovedEvent,
    PaymentFailedEvent,
)
from kafka_utils import (
    TraceAwareKafkaProducer,
    create_kafka_producer,
    start_consumer_span,
    with_correlation_id,
    with_trace_context,
)
from pydantic import ValidationError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.metrics import OrderMetrics
from app.outbox import OutboxMessage, OutboxRelay
from app.repository import OrderRepository
from app.refund_messaging import handle_refund_message
from app.store import (
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentEventOrderMissing,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentIgnored,
)

LOGGER: Final = logging.getLogger(__name__)


class OutboxPayloadError(RuntimeError):
    pass


class KafkaRuntimeConfigurationError(RuntimeError):
    pass


class KafkaOutboxPublisher:
    def __init__(self, producer: TraceAwareKafkaProducer) -> None:
        self._producer = producer

    async def start(self) -> None:
        await self._producer.start()

    async def stop(self) -> None:
        await self._producer.stop()

    async def publish(self, message: OutboxMessage) -> None:
        correlation_id = message.payload.get("correlationId")
        if not isinstance(correlation_id, str):
            raise OutboxPayloadError("outbox payload is missing correlationId")
        await self._producer.send_and_wait(
            message.topic,
            message.payload,
            with_correlation_id(correlation_id),
            with_trace_context(message.trace_context),
            key=message.message_key.encode("utf-8"),
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


@dataclass(slots=True)  # noqa: MUTABLE_OK
class _ConsumedKafkaMessage:
    """Mutable SDK-shaped message required by the tracing consumer protocol."""

    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


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
class KafkaRuntimeConfig:
    bootstrap_servers: str
    repository: OrderRepository
    session_factory: async_sessionmaker[AsyncSession] | None
    metrics: OrderMetrics


class PaymentConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: OrderRepository,
    ) -> None:
        self._consumer = consumer
        self._repository = repository

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_payment_message(message, self._repository)
            await self._consumer.commit()


type PaymentConsumerFactory = Callable[[], PaymentConsumer]
type OutboxWorkerFactory = Callable[[], tuple[KafkaOutboxPublisher, OutboxRelay]]


@dataclass(frozen=True, slots=True)
class KafkaRuntime:
    payment_consumer_factory: PaymentConsumerFactory | None
    outbox_worker_factory: OutboxWorkerFactory | None


async def handle_payment_message(
    message: KafkaMessage,
    repository: OrderRepository,
) -> None:
    # Kafka topic names are an open external set; unknown topics are rejected below.
    match message.topic:  # noqa: MATCH_OK
        case topic if topic == PAYMENT_APPROVED_TOPIC:
            await handle_payment_approved_message(message, repository)
        case topic if topic == PAYMENT_FAILED_TOPIC:
            await handle_payment_failed_message(message, repository)
        case topic if topic in (REFUND_COMPLETED_TOPIC, REFUND_FAILED_TOPIC):
            await handle_refund_message(message, repository)
        case _:
            return


async def handle_payment_approved_message(
    message: KafkaMessage,
    repository: OrderRepository,
) -> None:
    value = message.value
    if value is None:
        return
    with start_consumer_span(
        message,
        service_name="order-service",
        name="kafka.consume payment.approved",
    ):
        try:
            event = PaymentApprovedEvent.model_validate_json(value)
        except ValidationError:
            LOGGER.warning("discarded invalid payment.approved event")
            return
        result = await repository.apply_payment_approved(event)
        match result:
            case PaymentApplied() | PaymentAlreadyApplied():
                return
            case PaymentEventOrderMissing() | PaymentIgnored():
                return
            case unreachable:
                assert_never(unreachable)


async def handle_payment_failed_message(
    message: KafkaMessage,
    repository: OrderRepository,
) -> None:
    value = message.value
    if value is None:
        return
    with start_consumer_span(
        message,
        service_name="order-service",
        name="kafka.consume payment.failed",
        failure_code="payment_failed_event",
    ):
        try:
            event = PaymentFailedEvent.model_validate_json(value)
        except ValidationError:
            LOGGER.warning("discarded invalid payment.failed event")
            return
        result = await repository.apply_payment_failed(event)
        match result:
            case PaymentFailureApplied() | PaymentFailureAlreadyApplied():
                return
            case PaymentEventOrderMissing() | PaymentIgnored():
                return
            case unreachable:
                assert_never(unreachable)


def kafka_runtime_from_config(config: KafkaRuntimeConfig) -> KafkaRuntime:
    session_factory = config.session_factory
    if config.bootstrap_servers == "" or session_factory is None:
        return KafkaRuntime(
            payment_consumer_factory=None,
            outbox_worker_factory=None,
        )

    def payment_consumer_factory() -> PaymentConsumer:
        consumer = AIOKafkaConsumer(
            PAYMENT_APPROVED_TOPIC,
            PAYMENT_FAILED_TOPIC,
            REFUND_COMPLETED_TOPIC,
            REFUND_FAILED_TOPIC,
            bootstrap_servers=config.bootstrap_servers,
            group_id="order-service-payment-events",
            auto_offset_reset="earliest",
            enable_auto_commit=False,
        )
        return PaymentConsumer(
            AIOKafkaConsumerClient(consumer),
            config.repository,
        )

    def outbox_worker_factory() -> tuple[KafkaOutboxPublisher, OutboxRelay]:
        producer = create_kafka_producer(
            config.bootstrap_servers,
            client_id="order-service",
        )
        if producer is None:
            raise KafkaRuntimeConfigurationError(
                "Kafka producer requires bootstrap servers",
            )
        publisher = KafkaOutboxPublisher(producer)
        return (
            publisher,
            OutboxRelay(
                session_factory,
                publisher,
                config.metrics,
            ),
        )

    return KafkaRuntime(
        payment_consumer_factory=payment_consumer_factory,
        outbox_worker_factory=outbox_worker_factory,
    )
