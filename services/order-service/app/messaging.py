from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from typing import Protocol, assert_never

from aiokafka import AIOKafkaConsumer
from contracts import (
    NOTIFICATION_REQUESTED_TOPIC,
    ORDER_CREATED_TOPIC,
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    PaymentApprovedEvent,
    PaymentFailedEvent,
)
from kafka_utils import (
    TraceAwareKafkaProducer,
    create_kafka_producer,
    start_consumer_span,
    with_correlation_id,
)
from pydantic import ValidationError

from app.events import notification_requested_event, order_created_event
from app.models import IdempotencyKey, Order
from app.repository import OrderRepository
from app.store import (
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentEventOrderMissing,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentIgnored,
)


class OrderEventPublisher(Protocol):
    async def publish_order_created(
        self,
        order: Order,
        idempotency_key: IdempotencyKey,
    ) -> None: ...

    async def publish_notification_requested(self, order: Order) -> None: ...


class NoopOrderEventPublisher:
    async def publish_order_created(
        self,
        order: Order,
        idempotency_key: IdempotencyKey,
    ) -> None:
        return None

    async def publish_notification_requested(self, order: Order) -> None:
        return None


class KafkaOrderEventPublisher:
    def __init__(self, producer: TraceAwareKafkaProducer) -> None:
        self._producer = producer

    async def start(self) -> None:
        await self._producer.start()

    async def stop(self) -> None:
        await self._producer.stop()

    async def publish_order_created(
        self,
        order: Order,
        idempotency_key: IdempotencyKey,
    ) -> None:
        event = order_created_event(order, idempotency_key)
        await self._producer.send_and_wait(
            ORDER_CREATED_TOPIC,
            event.model_dump(mode="json"),
            with_correlation_id(event.correlationId),
            key=event.orderId.encode("utf-8"),
        )

    async def publish_notification_requested(self, order: Order) -> None:
        event = notification_requested_event(order)
        await self._producer.send_and_wait(
            NOTIFICATION_REQUESTED_TOPIC,
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

    async def commit(self) -> None: ...

    def __aiter__(self) -> AsyncIterator[KafkaMessage]: ...


@dataclass(frozen=True, slots=True)
class KafkaRuntime:
    publisher: KafkaOrderEventPublisher | None
    payment_consumer: "PaymentConsumer | None"


class PaymentConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: OrderRepository,
        event_publisher: OrderEventPublisher,
    ) -> None:
        self._consumer = consumer
        self._repository = repository
        self._event_publisher = event_publisher

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_payment_message(
                message,
                self._repository,
                self._event_publisher,
            )
            await self._consumer.commit()


async def handle_payment_message(
    message: KafkaMessage,
    repository: OrderRepository,
    event_publisher: OrderEventPublisher | None = None,
) -> None:
    match message.topic:
        case topic if topic == PAYMENT_APPROVED_TOPIC:
            await handle_payment_approved_message(message, repository, event_publisher)
        case topic if topic == PAYMENT_FAILED_TOPIC:
            await handle_payment_failed_message(message, repository)
        case _:
            return


async def handle_payment_approved_message(
    message: KafkaMessage,
    repository: OrderRepository,
    event_publisher: OrderEventPublisher | None = None,
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
            return
        result = await repository.apply_payment_approved(event)
        publisher = event_publisher or NoopOrderEventPublisher()
        match result:
            case PaymentApplied(order=order) | PaymentAlreadyApplied(order=order):
                await publisher.publish_notification_requested(order)
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
            return
        result = await repository.apply_payment_failed(event)
        match result:
            case PaymentFailureApplied() | PaymentFailureAlreadyApplied():
                return
            case PaymentEventOrderMissing() | PaymentIgnored():
                return
            case unreachable:
                assert_never(unreachable)


def kafka_runtime_from_bootstrap(
    repository: OrderRepository,
    bootstrap_servers: str,
) -> KafkaRuntime:
    if bootstrap_servers == "":
        return KafkaRuntime(publisher=None, payment_consumer=None)

    producer = create_kafka_producer(
        bootstrap_servers,
        client_id="order-service",
    )
    if producer is None:
        return KafkaRuntime(publisher=None, payment_consumer=None)

    consumer = AIOKafkaConsumer(
        PAYMENT_APPROVED_TOPIC,
        PAYMENT_FAILED_TOPIC,
        bootstrap_servers=bootstrap_servers,
        group_id="order-service-payment-events",
        auto_offset_reset="earliest",
        enable_auto_commit=False,
    )
    publisher = KafkaOrderEventPublisher(producer)
    return KafkaRuntime(
        publisher=publisher,
        payment_consumer=PaymentConsumer(consumer, repository, publisher),
    )
