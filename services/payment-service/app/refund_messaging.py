import logging
from collections.abc import Callable
from typing import Final

from aiokafka import AIOKafkaConsumer
from contracts import REFUND_REQUESTED_TOPIC, RefundRequestedEvent
from pydantic import ValidationError

from app.messaging import AIOKafkaConsumerClient, KafkaConsumerClient, KafkaMessage
from app.refunds import RefundRequestRepository

LOGGER: Final = logging.getLogger(__name__)


class RefundRequestedConsumer:
    def __init__(
        self,
        consumer: KafkaConsumerClient,
        repository: RefundRequestRepository,
    ) -> None:
        self._consumer: KafkaConsumerClient = consumer
        self._repository: RefundRequestRepository = repository

    async def start(self) -> None:
        await self._consumer.start()

    async def stop(self) -> None:
        await self._consumer.stop()

    async def run(self) -> None:
        async for message in self._consumer:
            await handle_refund_requested_message(message, self._repository)
            await self._consumer.commit()


type RefundRequestedConsumerFactory = Callable[[], RefundRequestedConsumer]


async def handle_refund_requested_message(
    message: KafkaMessage,
    repository: RefundRequestRepository,
) -> None:
    value = message.value
    if value is None:
        return
    try:
        event = RefundRequestedEvent.model_validate_json(value)
    except ValidationError:
        LOGGER.warning(
            "discarding invalid refund.requested payload partition=%d offset=%d",
            message.partition,
            message.offset,
        )
        return
    _ = await repository.record_refund_requested(event)


def refund_requested_consumer_factory(
    bootstrap_servers: str,
    repository: RefundRequestRepository | None,
) -> RefundRequestedConsumerFactory | None:
    if bootstrap_servers == "" or repository is None:
        return None

    def factory() -> RefundRequestedConsumer:
        consumer = AIOKafkaConsumer(
            REFUND_REQUESTED_TOPIC,
            bootstrap_servers=bootstrap_servers,
            group_id="payment-service-refund-requested",
            auto_offset_reset="earliest",
            enable_auto_commit=False,
        )
        return RefundRequestedConsumer(
            AIOKafkaConsumerClient(consumer),
            repository,
        )

    return factory
