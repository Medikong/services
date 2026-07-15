from typing import assert_never

from contracts import PaymentApprovedEvent, PaymentFailedEvent
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.events import (
    late_approval_refund_requested_event,
    notification_requested_event,
)
from app.models import OrderId, OrderStatus, PaymentId
from app.outbox import add_outbox_event
from app.postgres_inbox import record_processed_event
from app.postgres_inventory import (
    release_reserved_inventory,
    sell_reserved_inventory,
)
from app.postgres_mapping import order_from_record
from app.records import OrderRecord, OutboxEventRecord
from app.store import (
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentApprovalResult,
    PaymentEventOrderMissing,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentFailureResult,
    PaymentIgnored,
)


async def apply_payment_approved(
    session_factory: async_sessionmaker[AsyncSession],
    event: PaymentApprovedEvent,
) -> PaymentApprovalResult:
    """Apply one approved event with its Inbox and result Outbox atomically."""
    order_id = OrderId(event.orderId)
    valid_provenance = _payment_event_has_valid_provenance(event)
    async with session_factory() as session:
        record = await _locked_order(session, order_id)
        if record is None:
            if valid_provenance:
                await record_processed_event(session, event)
                await session.commit()
            return PaymentEventOrderMissing(order_id=order_id)
        status = OrderStatus(record.status)
        if not valid_provenance or not _payment_event_matches_order(record, event):
            return PaymentIgnored(order=order_from_record(record))
        processed = await record_processed_event(session, event)
        if not processed and status is not OrderStatus.EXPIRED:
            return _approval_replay_result(record, status)
        match status:
            case OrderStatus.PENDING_PAYMENT:
                await sell_reserved_inventory(session, record, event)
                record.status = OrderStatus.CONFIRMED.value
                record.payment_id = PaymentId(event.paymentId)
                record.confirmed_at = event.occurredAt
                add_outbox_event(
                    session,
                    notification_requested_event(order_from_record(record)),
                )
                await session.commit()
                return PaymentApplied(order=order_from_record(record))
            case OrderStatus.CONFIRMED:
                await session.commit()
                return PaymentAlreadyApplied(order=order_from_record(record))
            case OrderStatus.EXPIRED:
                expired_order = order_from_record(record)
                refund_event = late_approval_refund_requested_event(
                    expired_order,
                    event.paymentId,
                    event.occurredAt,
                )
                if await session.get(OutboxEventRecord, refund_event.eventId) is None:
                    add_outbox_event(
                        session,
                        refund_event,
                    )
                await session.commit()
                return PaymentIgnored(order=order_from_record(record))
            case (
                OrderStatus.PAYMENT_FAILED
                | OrderStatus.CANCEL_PENDING
                | OrderStatus.CANCELED
            ):
                await session.commit()
                return PaymentIgnored(order=order_from_record(record))
            case unreachable:
                assert_never(unreachable)


async def apply_payment_failed(
    session_factory: async_sessionmaker[AsyncSession],
    event: PaymentFailedEvent,
) -> PaymentFailureResult:
    """Apply one failed event with its Inbox and order transition atomically."""
    order_id = OrderId(event.orderId)
    valid_provenance = _payment_event_has_valid_provenance(event)
    async with session_factory() as session:
        record = await _locked_order(session, order_id)
        if record is None:
            if valid_provenance:
                await record_processed_event(session, event)
                await session.commit()
            return PaymentEventOrderMissing(order_id=order_id)
        status = OrderStatus(record.status)
        if not valid_provenance or not _payment_event_matches_order(record, event):
            return PaymentIgnored(order=order_from_record(record))
        if not await record_processed_event(session, event):
            return _failure_replay_result(record, status)
        match status:
            case OrderStatus.PENDING_PAYMENT:
                await release_reserved_inventory(session, record, event)
                record.status = OrderStatus.PAYMENT_FAILED.value
                record.payment_id = PaymentId(event.paymentId)
                await session.commit()
                return PaymentFailureApplied(order=order_from_record(record))
            case OrderStatus.PAYMENT_FAILED:
                await session.commit()
                return PaymentFailureAlreadyApplied(order=order_from_record(record))
            case (
                OrderStatus.CONFIRMED
                | OrderStatus.CANCEL_PENDING
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
                await session.commit()
                return PaymentIgnored(order=order_from_record(record))
            case unreachable:
                assert_never(unreachable)


async def _locked_order(session: AsyncSession, order_id: OrderId) -> OrderRecord | None:
    result = await session.execute(
        select(OrderRecord).where(OrderRecord.id == order_id).with_for_update(),
    )
    return result.scalar_one_or_none()


def _approval_replay_result(
    record: OrderRecord,
    status: OrderStatus,
) -> PaymentApprovalResult:
    match status:
        case OrderStatus.CONFIRMED:
            return PaymentAlreadyApplied(order=order_from_record(record))
        case (
            OrderStatus.PENDING_PAYMENT
            | OrderStatus.PAYMENT_FAILED
            | OrderStatus.CANCEL_PENDING
            | OrderStatus.CANCELED
            | OrderStatus.EXPIRED
        ):
            return PaymentIgnored(order=order_from_record(record))
        case unreachable:
            assert_never(unreachable)


def _payment_event_has_valid_provenance(
    event: PaymentApprovedEvent | PaymentFailedEvent,
) -> bool:
    return event.producer == "payment-service" and event.sourceId == event.paymentId


def _payment_event_matches_order(
    order: OrderRecord,
    event: PaymentApprovedEvent | PaymentFailedEvent,
) -> bool:
    """Validate order-dependent fields at the payment-service Kafka boundary.

    A self-consistent arbitrary payment ID cannot be distinguished locally;
    payment-service remains authoritative when executing a refund request.
    """
    return event.userId == order.user_id and event.amount == order.amount


def _failure_replay_result(
    record: OrderRecord,
    status: OrderStatus,
) -> PaymentFailureResult:
    match status:
        case OrderStatus.PAYMENT_FAILED:
            return PaymentFailureAlreadyApplied(order=order_from_record(record))
        case (
            OrderStatus.PENDING_PAYMENT
            | OrderStatus.CONFIRMED
            | OrderStatus.CANCEL_PENDING
            | OrderStatus.CANCELED
            | OrderStatus.EXPIRED
        ):
            return PaymentIgnored(order=order_from_record(record))
        case unreachable:
            assert_never(unreachable)
