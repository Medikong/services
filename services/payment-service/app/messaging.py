from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from typing import Protocol

from aiokafka import AIOKafkaConsumer
from contracts import (
    ORDER_CREATED_TOPIC,
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    OrderCreatedEvent,
)
from kafka_utils import (
    TraceAwareKafkaProducer,
    create_kafka_producer,
    start_consumer_span,
    with_correlation_id,
)
from pydantic import ValidationError

from app.events import payment_approved_event, payment_failed_event
from app.models import Payment
from app.repository import PaymentRepository


class PaymentEventPublisher(Protocol):
    async def publish_payment_approved(self, payment: Payment) -> None: ...

    async def publish_payment_failed(self, payment: Payment) -> None: ...


class NoopPaymentEventPublisher:
    async def publish_payment_approved(self, payment: Payment) -> None:
        return None

    async def publish_payment_failed(self, payment: Payment) -> None:
        return None


class PaymentEventPublisherRef:
    def __init__(self, current: PaymentEventPublisher | None = None) -> None:
        self.current = current or NoopPaymentEventPublisher()

    def replace(self, publisher: PaymentEventPublisher) -> None:
        self.current = publisher

    async def publish_payment_approved(self, payment: Payment) -> None:
        await self.current.publish_payment_approved(payment)

    async def publish_payment_failed(self, payment: Payment) -> None:
        await self.current.publish_payment_failed(payment)


class KafkaPaymentEventPublisher:
    def __init__(self, producer: TraceAwareKafkaProducer) -> None:
        self._producer = producer

    async def start(self) -> None:
        await self._producer.start()

    async def stop(self) -> None:
        await self._producer.stop()

    async def publish_payment_approved(self, payment: Payment) -> None:
        event = payment_approved_event(payment)
        await self._producer.send_and_wait(
            PAYMENT_APPROVED_TOPIC,
            event.model_dump(mode="json"),
            with_correlation_id(event.correlationId),
            key=event.orderId.encode("utf-8"),
        )

    async def publish_payment_failed(self, payment: Payment) -> None:
        event = payment_failed_event(payment)
        await self._producer.send_and_wait(
            PAYMENT_FAILED_TOPIC,
            event.model_dump(mode="json"),
            with_correlation_id(event.correlationId),
            key=event.orderId.encode("utf-8"),
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

    def __aiter__(self) -> AsyncIterator[KafkaMessage]: ...


@dataclass(frozen=True, slots=True)
class KafkaRuntime:
    publisher: KafkaPaymentEventPublisher | None
    order_created_consumer: "OrderCreatedConsumer | None"


class OrderCreatedConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: PaymentRepository,
    ) -> None:
        self._consumer = consumer
        self._repository = repository

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_order_created_message(message, self._repository)


async def handle_order_created_message(
    message: KafkaMessage,
    repository: PaymentRepository,
) -> None:
    value = message.value
    if value is None:
        return
    with start_consumer_span(message, name="kafka.consume order.created"):
        try:
            event = OrderCreatedEvent.model_validate_json(value)
        except ValidationError:
            return
        await repository.record_order_created(event)


def kafka_runtime_from_bootstrap(
    repository: PaymentRepository,
    bootstrap_servers: str,
) -> KafkaRuntime:
    if bootstrap_servers == "":
        return KafkaRuntime(publisher=None, order_created_consumer=None)

    producer = create_kafka_producer(
        bootstrap_servers,
        client_id="payment-service",
    )
    if producer is None:
        return KafkaRuntime(publisher=None, order_created_consumer=None)

    consumer = AIOKafkaConsumer(
        ORDER_CREATED_TOPIC,
        bootstrap_servers=bootstrap_servers,
        group_id="payment-service-order-created",
        auto_offset_reset="earliest",
        enable_auto_commit=True,
    )
    return KafkaRuntime(
        publisher=KafkaPaymentEventPublisher(producer),
        order_created_consumer=OrderCreatedConsumer(consumer, repository),
    )
