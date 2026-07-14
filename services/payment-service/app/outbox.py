from collections.abc import Mapping
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Final, Protocol

import anyio
from aiokafka.errors import KafkaError
from contracts import (
    PaymentApprovedEvent,
    PaymentFailedEvent,
    RefundCompletedEvent,
    RefundFailedEvent,
)
from observability import capture_current_trace_context
from sqlalchemy import or_, select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.metrics import OutboxRelayOutcome, PaymentMetrics
from app.records import JsonValue, OutboxEventRecord

MAX_RETRY_DELAY: Final = timedelta(minutes=5)
MAX_RELAY_ATTEMPTS: Final = 10
MAX_ERROR_LENGTH: Final = 500
RELAY_IDLE_DELAY_SECONDS: Final = 0.25
type RelayPayload = Mapping[str, str | int | bool | None]
type TraceContextPayload = Mapping[str, JsonValue]


@dataclass(frozen=True, slots=True)
class OutboxMessage:
    event_id: str
    topic: str
    message_key: str
    payload: RelayPayload
    trace_context: TraceContextPayload | None = None


@dataclass(frozen=True, slots=True)
class OutboxAggregate:
    aggregate_type: str
    aggregate_id: str


class OutboxPublisher(Protocol):
    async def publish(self, message: OutboxMessage) -> None: ...


class OutboxRelay:
    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        publisher: OutboxPublisher,
        metrics: PaymentMetrics,
    ) -> None:
        self._session_factory = session_factory
        self._publisher = publisher
        self._metrics = metrics

    async def relay_once(self, now: datetime | None = None) -> bool:
        """Publish at most one due event while holding its row lock."""
        attempted_at = now or datetime.now(UTC)
        async with self._session_factory.begin() as session:
            record = (
                await session.execute(_claim_statement(attempted_at))
            ).scalar_one_or_none()
            if record is None:
                return False
            message = OutboxMessage(
                event_id=record.event_id,
                topic=record.topic,
                message_key=record.message_key,
                payload=record.payload,
                trace_context=record.trace_context,
            )
            try:
                await self._publisher.publish(message)
            except (KafkaError, OSError, RuntimeError) as error:
                record.attempts += 1
                record.last_error = _bounded_error(error)
                if record.attempts >= MAX_RELAY_ATTEMPTS:
                    record.dead_lettered_at = attempted_at
                    record.next_attempt_at = None
                    outcome = OutboxRelayOutcome.DEAD_LETTERED
                else:
                    record.next_attempt_at = attempted_at + retry_delay(record.attempts)
                    outcome = OutboxRelayOutcome.RETRY
            else:
                record.published_at = attempted_at
                outcome = OutboxRelayOutcome.PUBLISHED
        self._metrics.record_outbox_relay(outcome)
        return True

    async def run(self) -> None:
        """Relay due rows until the enclosing task group cancels the worker."""
        while True:
            attempted = await self.relay_once()
            if not attempted:
                await anyio.sleep(RELAY_IDLE_DELAY_SECONDS)


def retry_delay(attempt: int) -> timedelta:
    """Return bounded exponential backoff for a one-based attempt number."""
    return min(timedelta(seconds=1 << (attempt - 1)), MAX_RETRY_DELAY)


def add_payment_outbox_event(
    session: AsyncSession,
    event: PaymentApprovedEvent | PaymentFailedEvent,
) -> None:
    """Stage a terminal payment event in the caller's transaction."""
    _add_outbox_event(
        session,
        event,
        OutboxAggregate(aggregate_type="payment", aggregate_id=event.paymentId),
    )


def add_refund_outbox_event(
    session: AsyncSession,
    event: RefundCompletedEvent | RefundFailedEvent,
) -> None:
    _add_outbox_event(
        session,
        event,
        OutboxAggregate(aggregate_type="refund", aggregate_id=event.refundId),
    )


def _add_outbox_event(
    session: AsyncSession,
    event: (
        PaymentApprovedEvent
        | PaymentFailedEvent
        | RefundCompletedEvent
        | RefundFailedEvent
    ),
    aggregate: OutboxAggregate,
) -> None:
    trace_context = capture_current_trace_context()
    session.add(
        OutboxEventRecord(
            event_id=event.eventId,
            event_type=event.eventType,
            aggregate_type=aggregate.aggregate_type,
            aggregate_id=aggregate.aggregate_id,
            topic=event.eventType,
            message_key=event.orderId,
            payload=event.model_dump(mode="json"),
            trace_context=(
                trace_context.as_dict() if trace_context is not None else None
            ),
            occurred_at=event.occurredAt,
            attempts=0,
        ),
    )


def _claim_statement(now: datetime):
    return (
        select(OutboxEventRecord)
        .where(
            OutboxEventRecord.published_at.is_(None),
            OutboxEventRecord.dead_lettered_at.is_(None),
            or_(
                OutboxEventRecord.next_attempt_at.is_(None),
                OutboxEventRecord.next_attempt_at <= now,
            ),
        )
        .order_by(OutboxEventRecord.occurred_at, OutboxEventRecord.event_id)
        .limit(1)
        .with_for_update(skip_locked=True)
    )


def _bounded_error(error: KafkaError | OSError | RuntimeError) -> str:
    return f"{type(error).__name__}: {error}"[:MAX_ERROR_LENGTH]
