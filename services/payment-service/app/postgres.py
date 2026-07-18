from datetime import UTC, datetime

from contracts import OrderCreatedEvent
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.events import payment_approved_event, payment_failed_event
from app.models import OrderId, Payment, PaymentId, UserId
from app.outbox import add_payment_outbox_event
from app.postgres_mapping import (
    known_order_from_record,
    new_approved_payment,
    new_failed_payment,
    payment_from_record,
    record_from_payment,
)
from app.records import Base as Base
from app.records import KnownOrderRecord, PaymentRecord, ProcessedEventRecord
from app.store import (
    ApprovePaymentCommand,
    ApprovePaymentResult,
    FailPaymentCommand,
    FailPaymentResult,
    KnownOrder,
    PaymentAlreadyApproved,
    PaymentAlreadyFailed,
    PaymentApproved,
    PaymentFailed,
    PaymentIdempotencyConflict,
    PaymentOrderMismatch,
    PaymentOrderNotFound,
    PaymentOrderOwnerMismatch,
    PaymentTerminalConflict,
    failed_payment_matches_command,
    payment_matches_command,
)


class PostgresPaymentRepository:
    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def approve_mock_payment(
        self,
        command: ApprovePaymentCommand,
    ) -> ApprovePaymentResult:
        async with self._session_factory() as session:
            try:
                async with session.begin():
                    known_order = await _locked_known_order(session, command.order_id)
                    if known_order is None:
                        return PaymentOrderNotFound(order_id=command.order_id)
                    projected_order = known_order_from_record(known_order)
                    if projected_order.user_id != command.user_id:
                        return PaymentOrderOwnerMismatch(order_id=command.order_id)
                    replayed = await _replayed_payment(session, command)
                    if replayed is not None:
                        return _approval_replay(replayed, command)
                    terminal = await _terminal_payment(session, command.order_id)
                    if terminal is not None:
                        return PaymentTerminalConflict(payment=terminal)
                    if projected_order.amount != command.amount:
                        return PaymentOrderMismatch(order_id=command.order_id)
                    payment = new_approved_payment(command)
                    session.add(record_from_payment(payment, command.idempotency_key))
                    add_payment_outbox_event(session, payment_approved_event(payment))
            except IntegrityError:
                conflict = await self._approval_conflict(command)
                if conflict is not None:
                    return conflict
                raise
        return PaymentApproved(payment=payment)

    async def fail_mock_payment(self, command: FailPaymentCommand) -> FailPaymentResult:
        async with self._session_factory() as session:
            try:
                async with session.begin():
                    known_order = await _locked_known_order(session, command.order_id)
                    if known_order is None:
                        return PaymentOrderNotFound(order_id=command.order_id)
                    projected_order = known_order_from_record(known_order)
                    if projected_order.user_id != command.user_id:
                        return PaymentOrderOwnerMismatch(order_id=command.order_id)
                    replayed = await _replayed_payment(session, command)
                    if replayed is not None:
                        return _failure_replay(replayed, command)
                    terminal = await _terminal_payment(session, command.order_id)
                    if terminal is not None:
                        return PaymentTerminalConflict(payment=terminal)
                    if projected_order.amount != command.amount:
                        return PaymentOrderMismatch(order_id=command.order_id)
                    payment = new_failed_payment(command)
                    session.add(record_from_payment(payment, command.idempotency_key))
                    add_payment_outbox_event(session, payment_failed_event(payment))
            except IntegrityError:
                conflict = await self._failure_conflict(command)
                if conflict is not None:
                    return conflict
                raise
        return PaymentFailed(payment=payment)

    async def get_payment(self, payment_id: PaymentId) -> Payment | None:
        async with self._session_factory() as session:
            record = await session.get(PaymentRecord, payment_id)
            return None if record is None else payment_from_record(record)

    async def record_order_created(self, event: OrderCreatedEvent) -> KnownOrder:
        async with self._session_factory.begin() as session:
            claimed_event_id = (
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
            if claimed_event_id is not None:
                await session.execute(
                    pg_insert(KnownOrderRecord)
                    .values(
                        order_id=event.orderId,
                        user_id=event.userId,
                        amount=event.amount,
                        created_at=event.occurredAt,
                    )
                    .on_conflict_do_nothing(index_elements=["order_id"]),
                )
            record = await session.get(KnownOrderRecord, event.orderId)
            if record is None:
                return KnownOrder(
                    order_id=OrderId(event.orderId),
                    user_id=UserId(event.userId),
                    amount=event.amount,
                )
            return known_order_from_record(record)

    async def get_known_order(self, order_id: str) -> KnownOrder | None:
        async with self._session_factory() as session:
            record = await session.get(KnownOrderRecord, order_id)
            return None if record is None else known_order_from_record(record)

    async def _approval_conflict(
        self,
        command: ApprovePaymentCommand,
    ) -> ApprovePaymentResult | None:
        async with self._session_factory() as session:
            replayed = await _replayed_payment(session, command)
            if replayed is not None:
                return _approval_replay(replayed, command)
            terminal = await _terminal_payment(session, command.order_id)
            return None if terminal is None else PaymentTerminalConflict(terminal)

    async def _failure_conflict(
        self,
        command: FailPaymentCommand,
    ) -> FailPaymentResult | None:
        async with self._session_factory() as session:
            replayed = await _replayed_payment(session, command)
            if replayed is not None:
                return _failure_replay(replayed, command)
            terminal = await _terminal_payment(session, command.order_id)
            return None if terminal is None else PaymentTerminalConflict(terminal)


async def _locked_known_order(
    session: AsyncSession,
    order_id: OrderId,
) -> KnownOrderRecord | None:
    return (
        await session.execute(
            select(KnownOrderRecord)
            .where(KnownOrderRecord.order_id == order_id)
            .with_for_update(),
        )
    ).scalar_one_or_none()


async def _replayed_payment(
    session: AsyncSession,
    command: ApprovePaymentCommand | FailPaymentCommand,
) -> Payment | None:
    record = (
        await session.execute(
            select(PaymentRecord).where(
                PaymentRecord.user_id == command.user_id,
                PaymentRecord.idempotency_key == command.idempotency_key,
            ),
        )
    ).scalar_one_or_none()
    return None if record is None else payment_from_record(record)


async def _terminal_payment(session: AsyncSession, order_id: OrderId) -> Payment | None:
    record = (
        await session.execute(
            select(PaymentRecord).where(PaymentRecord.order_id == order_id),
        )
    ).scalar_one_or_none()
    return None if record is None else payment_from_record(record)


def _approval_replay(
    payment: Payment,
    command: ApprovePaymentCommand,
) -> ApprovePaymentResult:
    if payment_matches_command(payment, command):
        return PaymentAlreadyApproved(payment=payment)
    return PaymentIdempotencyConflict(payment=payment)


def _failure_replay(payment: Payment, command: FailPaymentCommand) -> FailPaymentResult:
    if failed_payment_matches_command(payment, command):
        return PaymentAlreadyFailed(payment=payment)
    return PaymentIdempotencyConflict(payment=payment)
