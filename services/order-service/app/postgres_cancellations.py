from datetime import UTC, datetime
from typing import assert_never
from uuid import uuid4

from contracts import FulfillmentStatus, RefundStatus
from sqlalchemy import or_, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.cancellations import (
    CancellationAlreadyRequested,
    CancellationIdempotencyConflict,
    CancellationNotAllowed,
    CancellationOrderMissing,
    CancellationOwnerMismatch,
    CancellationRequested,
    RequestCancellationCommand,
    RequestCancellationResult,
)
from app.events import refund_requested_event
from app.models import Cancellation, OrderStatus, UserId
from app.outbox import add_outbox_event
from app.postgres_mapping import order_from_record
from app.records import CancellationRequestRecord, OrderRecord


async def request_cancellation(
    session_factory: async_sessionmaker[AsyncSession],
    command: RequestCancellationCommand,
) -> RequestCancellationResult:
    async with session_factory() as session:
        order = await _locked_order(session, command)
        if order is None:
            return CancellationOrderMissing(order_id=command.order_id)
        if order.user_id != command.user_id:
            return CancellationOwnerMismatch(order_id=command.order_id)

        existing = await _matching_cancellation(session, command)
        if existing is not None:
            return _replay_result(existing, order, command)

        status = OrderStatus(order.status)
        fulfillment = FulfillmentStatus(order.fulfillment_status)
        match status:
            case OrderStatus.CONFIRMED:
                match fulfillment:
                    case FulfillmentStatus.NOT_STARTED | FulfillmentStatus.PREPARING:
                        pass
                    case FulfillmentStatus.SHIPPED:
                        return CancellationNotAllowed(order=order_from_record(order))
                    case unreachable_fulfillment:
                        assert_never(unreachable_fulfillment)
            case (
                OrderStatus.PENDING_PAYMENT
                | OrderStatus.PAYMENT_FAILED
                | OrderStatus.CANCEL_PENDING
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
                return CancellationNotAllowed(order=order_from_record(order))
            case unreachable_status:
                assert_never(unreachable_status)

        payment_id = order.payment_id
        if payment_id is None:
            return CancellationNotAllowed(order=order_from_record(order))
        requested_at = datetime.now(UTC)
        order.status = OrderStatus.CANCEL_PENDING.value
        order.cancel_pending_at = requested_at
        record = CancellationRequestRecord(
            id=f"cancellation-{uuid4().hex[:12]}",
            order_id=order.id,
            user_id=order.user_id,
            idempotency_key=command.idempotency_key,
            reason=command.reason,
            refund_status=RefundStatus.REQUESTED.value,
            created_at=requested_at,
            updated_at=requested_at,
        )
        session.add(record)
        cancellation = _cancellation_from_records(record, order)
        add_outbox_event(
            session,
            refund_requested_event(
                cancellation,
                order_from_record(order),
                payment_id,
            ),
        )
        try:
            await session.commit()
        except IntegrityError:
            await session.rollback()
            replayed = await _matching_cancellation(session, command)
            if replayed is None:
                raise
            replayed_order = await session.get(OrderRecord, replayed.order_id)
            if replayed_order is None:
                raise
            return _replay_result(replayed, replayed_order, command)
        return CancellationRequested(cancellation=cancellation)


async def get_cancellation(
    session_factory: async_sessionmaker[AsyncSession],
    order_id: str,
    user_id: UserId,
) -> Cancellation | None:
    async with session_factory() as session:
        row = (
            await session.execute(
                select(CancellationRequestRecord, OrderRecord)
                .join(
                    OrderRecord,
                    OrderRecord.id == CancellationRequestRecord.order_id,
                )
                .where(
                    CancellationRequestRecord.order_id == order_id,
                    OrderRecord.user_id == user_id,
                ),
            )
        ).one_or_none()
        if row is None:
            return None
        record, order = row
        return _cancellation_from_records(record, order)


async def _locked_order(
    session: AsyncSession,
    command: RequestCancellationCommand,
) -> OrderRecord | None:
    return (
        await session.execute(
            select(OrderRecord)
            .where(OrderRecord.id == command.order_id)
            .with_for_update(),
        )
    ).scalar_one_or_none()


async def _matching_cancellation(
    session: AsyncSession,
    command: RequestCancellationCommand,
) -> CancellationRequestRecord | None:
    return (
        await session.execute(
            select(CancellationRequestRecord)
            .where(
                or_(
                    CancellationRequestRecord.order_id == command.order_id,
                    (CancellationRequestRecord.user_id == command.user_id)
                    & (
                        CancellationRequestRecord.idempotency_key
                        == command.idempotency_key
                    ),
                ),
            )
            .limit(1)
            .with_for_update(),
        )
    ).scalar_one_or_none()


def _replay_result(
    record: CancellationRequestRecord,
    order: OrderRecord,
    command: RequestCancellationCommand,
) -> RequestCancellationResult:
    cancellation = Cancellation(
        id=record.id,
        orderId=record.order_id,
        reason=record.reason,
        orderStatus=OrderStatus.CANCEL_PENDING,
        refundStatus=RefundStatus.REQUESTED,
        createdAt=record.created_at,
    )
    if (
        record.order_id == command.order_id
        and record.user_id == command.user_id
        and record.idempotency_key == command.idempotency_key
        and record.reason == command.reason
    ):
        return CancellationAlreadyRequested(cancellation=cancellation)
    return CancellationIdempotencyConflict(cancellation=cancellation)


def _cancellation_from_records(
    record: CancellationRequestRecord,
    order: OrderRecord,
) -> Cancellation:
    return Cancellation(
        id=record.id,
        orderId=record.order_id,
        reason=record.reason,
        orderStatus=OrderStatus(order.status),
        refundStatus=RefundStatus(record.refund_status),
        createdAt=record.created_at,
        completedAt=order.canceled_at,
    )
