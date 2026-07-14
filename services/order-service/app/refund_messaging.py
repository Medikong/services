import logging
from typing import Final, Protocol, assert_never

from contracts import (
    REFUND_COMPLETED_TOPIC,
    REFUND_FAILED_TOPIC,
    RefundCompletedEvent,
    RefundFailedEvent,
)
from pydantic import ValidationError


LOGGER: Final = logging.getLogger(__name__)


class RefundKafkaMessage(Protocol):
    topic: str
    partition: int
    offset: int
    value: bytes | None


class RefundEventRepository(Protocol):
    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool: ...

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool: ...


async def handle_refund_message(
    message: RefundKafkaMessage,
    repository: RefundEventRepository,
) -> None:
    value = message.value
    if value is None:
        return
    try:
        match message.topic:  # noqa: MATCH_OK
            case topic if topic == REFUND_COMPLETED_TOPIC:
                event = RefundCompletedEvent.model_validate_json(value)
            case topic if topic == REFUND_FAILED_TOPIC:
                event = RefundFailedEvent.model_validate_json(value)
            case _:
                return
    except ValidationError:
        LOGGER.warning("discarded invalid %s event", message.topic)
        return

    match event:
        case RefundCompletedEvent():
            _ = await repository.apply_refund_completed(event)
        case RefundFailedEvent():
            _ = await repository.apply_refund_failed(event)
        case unreachable:
            assert_never(unreachable)
