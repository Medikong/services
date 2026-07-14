from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime

import anyio
import pytest
from contracts import RefundCompletedEvent, RefundFailedEvent
from pydantic import ValidationError

from app.messaging import handle_payment_message


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


@dataclass(slots=True)  # noqa: MUTABLE_OK
class RecordingRefundRepository:
    completed: list[str] = field(default_factory=list)
    failed: list[str] = field(default_factory=list)

    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool:
        self.completed.append(event.eventId)
        return True

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool:
        self.failed.append(event.eventId)
        return True


class RepositoryValidationFailure:
    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool:
        RefundCompletedEvent.model_validate({})
        return False

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool:
        return False


@pytest.mark.parametrize(
    ("event", "recorded_attribute"),
    [
        (
            RefundCompletedEvent(
                eventId="evt-refund-completed-message",
                userId="user-message",
                sourceId="refund-message",
                occurredAt=datetime(2026, 7, 15, 3, 0, tzinfo=UTC),
                producer="payment-service",
                refundId="refund-message",
                orderId="order-message",
                paymentId="payment-message",
                amount=50000,
            ),
            "completed",
        ),
        (
            RefundFailedEvent(
                eventId="evt-refund-failed-message",
                userId="user-message",
                sourceId="refund-message",
                occurredAt=datetime(2026, 7, 15, 3, 1, tzinfo=UTC),
                producer="payment-service",
                refundId="refund-message",
                orderId="order-message",
                paymentId="payment-message",
                amount=50000,
                reason="provider failure",
            ),
            "failed",
        ),
    ],
)
def test_refund_result_topic_is_dispatched(
    event: RefundCompletedEvent | RefundFailedEvent,
    recorded_attribute: str,
) -> None:
    # Given
    repository = RecordingRefundRepository()

    # When
    anyio.run(
        handle_payment_message,
        FakeKafkaMessage(
            topic=event.eventType,
            partition=0,
            offset=1,
            headers=None,
            value=event.model_dump_json().encode(),
        ),
        repository,
    )

    # Then
    assert getattr(repository, recorded_attribute) == [event.eventId]


def test_repository_validation_error_propagates_before_offset_commit() -> None:
    # Given
    event = RefundCompletedEvent(
        eventId="evt-refund-repository-validation",
        userId="user-message",
        sourceId="refund-message",
        occurredAt=datetime(2026, 7, 15, 3, 2, tzinfo=UTC),
        producer="payment-service",
        refundId="refund-message",
        orderId="order-message",
        paymentId="payment-message",
        amount=50000,
    )
    message = FakeKafkaMessage(
        topic=event.eventType,
        partition=0,
        offset=2,
        headers=None,
        value=event.model_dump_json().encode(),
    )

    # When / Then
    with pytest.raises(ValidationError):
        anyio.run(handle_payment_message, message, RepositoryValidationFailure())
