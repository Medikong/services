from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from typing import Protocol, assert_never

from aiokafka import AIOKafkaConsumer
from contracts import NOTIFICATION_REQUESTED_TOPIC, NotificationRequestedEvent
from kafka_utils import start_consumer_span
from pydantic import ValidationError

from app.metrics import NotificationMetrics
from app.repository import NotificationRepository
from app.store import NotificationAlreadyRecorded, NotificationRecorded


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
    notification_requested_consumer: "NotificationRequestedConsumer | None"


class NotificationRequestedConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: NotificationRepository,
        metrics: NotificationMetrics,
    ) -> None:
        self._consumer = consumer
        self._repository = repository
        self._metrics = metrics

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_notification_requested_message(message, self._repository, self._metrics)


async def handle_notification_requested_message(
    message: KafkaMessage,
    repository: NotificationRepository,
    metrics: NotificationMetrics,
) -> None:
    value = message.value
    if value is None:
        metrics.record_invalid()
        return
    with start_consumer_span(
        message,
        service_name="notification-service",
        name="kafka.consume notification.requested",
    ):
        try:
            event = NotificationRequestedEvent.model_validate_json(value)
            result = await repository.record_notification_requested(event)
            match result:
                case NotificationRecorded():
                    metrics.record_created()
                case NotificationAlreadyRecorded():
                    metrics.record_replayed()
                case unreachable:
                    assert_never(unreachable)
        except ValidationError:
            metrics.record_invalid()
            return


def kafka_runtime_from_bootstrap(
    repository: NotificationRepository,
    bootstrap_servers: str,
    metrics: NotificationMetrics,
) -> KafkaRuntime:
    if bootstrap_servers == "":
        return KafkaRuntime(notification_requested_consumer=None)

    consumer = AIOKafkaConsumer(
        NOTIFICATION_REQUESTED_TOPIC,
        bootstrap_servers=bootstrap_servers,
        group_id="notification-service-notification-requested",
        auto_offset_reset="earliest",
        enable_auto_commit=True,
    )
    return KafkaRuntime(
        notification_requested_consumer=NotificationRequestedConsumer(consumer, repository, metrics),
    )
