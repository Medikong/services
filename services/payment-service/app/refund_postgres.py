from datetime import UTC, datetime, timedelta
from hashlib import sha256
from typing import Final

from contracts import RefundRequestedEvent
from sqlalchemy import and_, or_, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.ledger import RefundRecord
from app.outbox import add_refund_outbox_event
from app.records import KnownOrderRecord, PaymentRecord, ProcessedEventRecord
from app.refund_events import refund_completed_event, refund_failed_event
from app.refunds import (
    REFUND_COMPLETED,
    REFUND_FAILED,
    REFUND_PROCESSING,
    REFUND_REQUESTED,
    RefundAttempt,
)

MAX_ERROR_LENGTH: Final = 500
MAX_RETRY_DELAY: Final = timedelta(minutes=5)
PROCESSING_LEASE: Final = timedelta(minutes=5)


class PostgresRefundRepository:
    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        max_attempts: int,
    ) -> None:
        self._session_factory: async_sessionmaker[AsyncSession] = session_factory
        self._max_attempts: int = max_attempts

    async def record_refund_requested(self, event: RefundRequestedEvent) -> bool:
        if not _event_fits_storage(event):
            return False
        fingerprint = _refund_fingerprint(event)
        async with self._session_factory.begin() as session:
            payment = (
                await session.execute(
                    select(PaymentRecord)
                    .where(PaymentRecord.id == event.paymentId)
                    .with_for_update(),
                )
            ).scalar_one_or_none()
            known_order = await session.get(KnownOrderRecord, event.orderId)
            if payment is None or known_order is None:
                return False
            if not _approved_payment_matches(event, payment, known_order):
                return False
            existing = (
                await session.execute(
                    select(RefundRecord.id)
                    .where(
                        or_(
                            RefundRecord.id == event.refundId,
                            RefundRecord.order_id == event.orderId,
                            RefundRecord.payment_id == event.paymentId,
                            RefundRecord.idempotency_fingerprint == fingerprint,
                        ),
                    )
                    .limit(1)
                    .with_for_update(),
                )
            ).scalar_one_or_none()
            if existing is not None:
                return False
            claimed_event = (
                await session.execute(
                    pg_insert(ProcessedEventRecord)
                    .values(
                        event_id=event.eventId,
                        event_type=event.eventType,
                        processed_at=datetime.now(UTC),
                    )
                    .on_conflict_do_nothing(index_elements=["event_id"])
                    .returning(ProcessedEventRecord.event_id),
                )
            ).scalar_one_or_none()
            if claimed_event is None:
                return False
            session.add(
                RefundRecord(
                    id=event.refundId,
                    order_id=event.orderId,
                    payment_id=event.paymentId,
                    user_id=event.userId,
                    amount=payment.amount,
                    status=REFUND_REQUESTED,
                    reason=event.reason,
                    idempotency_fingerprint=fingerprint,
                    attempts=0,
                    created_at=event.occurredAt,
                    updated_at=event.occurredAt,
                ),
            )
        return True

    async def claim_due_refund(self, now: datetime) -> RefundAttempt | None:
        async with self._session_factory.begin() as session:
            record = (
                await session.execute(
                    select(RefundRecord)
                    .where(
                        or_(
                            and_(
                                RefundRecord.status.in_(
                                    (REFUND_REQUESTED, REFUND_FAILED),
                                ),
                                RefundRecord.attempts < self._max_attempts,
                                or_(
                                    RefundRecord.next_attempt_at.is_(None),
                                    RefundRecord.next_attempt_at <= now,
                                ),
                            ),
                            and_(
                                RefundRecord.status == REFUND_PROCESSING,
                                RefundRecord.attempts <= self._max_attempts,
                                RefundRecord.updated_at <= now - PROCESSING_LEASE,
                            ),
                        ),
                    )
                    .order_by(RefundRecord.created_at, RefundRecord.id)
                    .limit(1)
                    .with_for_update(skip_locked=True),
                )
            ).scalar_one_or_none()
            if record is None:
                return None
            if record.status != REFUND_PROCESSING:
                record.attempts += 1
            record.status = REFUND_PROCESSING
            record.updated_at = now
            record.next_attempt_at = None
            record.last_error = None
            return _attempt_from_record(record)

    async def complete_refund(self, attempt: RefundAttempt, now: datetime) -> bool:
        async with self._session_factory.begin() as session:
            record = await _locked_refund(session, attempt.refund_id)
            if record is None:
                return False
            if not _is_current_attempt(record, attempt):
                return False
            record.status = REFUND_COMPLETED
            record.updated_at = now
            record.completed_at = now
            record.last_error = None
            record.next_attempt_at = None
            add_refund_outbox_event(session, refund_completed_event(attempt, now))
        return True

    async def fail_refund(
        self,
        attempt: RefundAttempt,
        reason: str,
        now: datetime,
    ) -> bool:
        bounded_reason = (reason or "RefundProviderFailed")[:MAX_ERROR_LENGTH]
        async with self._session_factory.begin() as session:
            record = await _locked_refund(session, attempt.refund_id)
            if record is None:
                return False
            if not _is_current_attempt(record, attempt):
                return False
            record.status = REFUND_FAILED
            record.updated_at = now
            record.last_error = bounded_reason
            if record.attempts >= self._max_attempts:
                record.next_attempt_at = None
                add_refund_outbox_event(
                    session,
                    refund_failed_event(attempt, bounded_reason, now),
                )
            else:
                record.next_attempt_at = now + refund_retry_delay(record.attempts)
        return True


def refund_retry_delay(attempt: int) -> timedelta:
    return min(timedelta(seconds=1 << (attempt - 1)), MAX_RETRY_DELAY)


async def _locked_refund(
    session: AsyncSession,
    refund_id: str,
) -> RefundRecord | None:
    return (
        await session.execute(
            select(RefundRecord).where(RefundRecord.id == refund_id).with_for_update(),
        )
    ).scalar_one_or_none()


def _approved_payment_matches(
    event: RefundRequestedEvent,
    payment: PaymentRecord,
    known_order: KnownOrderRecord,
) -> bool:
    return (
        payment.status == "APPROVED"
        and payment.order_id == event.orderId
        and payment.user_id == event.userId
        and payment.amount == event.amount
        and known_order.user_id == event.userId
        and known_order.amount == payment.amount
        and event.sourceId == event.orderId
    )


def _event_fits_storage(event: RefundRequestedEvent) -> bool:
    return (
        _fits_text(event.eventId, 128)
        and _fits_text(event.eventType, 128)
        and _fits_text(event.refundId, 64)
        and _fits_text(event.orderId, 64)
        and _fits_text(event.paymentId, 64)
        and _fits_text(event.userId, 64)
        and _fits_text(event.reason, 500)
        and event.occurredAt.tzinfo is not None
        and event.occurredAt.utcoffset() is not None
    )


def _fits_text(value: str, max_length: int) -> bool:
    return 0 < len(value) <= max_length and "\x00" not in value


def _refund_fingerprint(event: RefundRequestedEvent) -> str:
    canonical = "\x1f".join(
        (
            event.refundId,
            event.orderId,
            event.paymentId,
            event.userId,
            str(event.amount),
            event.reason,
        ),
    )
    return sha256(canonical.encode()).hexdigest()


def _attempt_from_record(record: RefundRecord) -> RefundAttempt:
    return RefundAttempt(
        refund_id=record.id,
        order_id=record.order_id,
        payment_id=record.payment_id,
        user_id=record.user_id,
        amount=record.amount,
        attempt=record.attempts,
    )


def _is_current_attempt(
    record: RefundRecord,
    attempt: RefundAttempt,
) -> bool:
    return record.status == REFUND_PROCESSING and record.attempts == attempt.attempt
