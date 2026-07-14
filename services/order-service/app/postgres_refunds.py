from typing import assert_never

from contracts import (
    RefundCompletedEvent,
    RefundFailedEvent,
    RefundStatus,
)
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.events import (
    inventory_changed_event,
    order_canceled_notification_event,
    refund_failed_notification_event,
)
from app.models import OrderId, OrderStatus
from app.outbox import add_outbox_event
from app.postgres_inbox import record_processed_event
from app.postgres_mapping import order_from_record
from app.records import CancellationRequestRecord, InventoryItemRecord, OrderRecord
from app.refund_validation import refund_event_is_valid


async def apply_refund_completed(
    session_factory: async_sessionmaker[AsyncSession],
    event: RefundCompletedEvent,
) -> bool:
    if not refund_event_is_valid(event):
        return False
    async with session_factory() as session:
        order = await _locked_order(session, OrderId(event.orderId))
        if order is None:
            return False
        cancellation = await _locked_cancellation(session, order.id)
        if cancellation is None or not _refund_matches(event, cancellation, order):
            await session.commit()
            return False
        if not await record_processed_event(session, event):
            return False
        match OrderStatus(order.status):
            case OrderStatus.CANCEL_PENDING:
                inventory = await _locked_inventory(session, order)
                inventory.sold_quantity -= order.quantity
                inventory.version += 1
                order.status = OrderStatus.CANCELED.value
                order.canceled_at = event.occurredAt
                cancellation.refund_status = RefundStatus.COMPLETED.value
                cancellation.updated_at = event.occurredAt
                canceled_order = order_from_record(order)
                add_outbox_event(
                    session,
                    inventory_changed_event(
                        inventory,
                        cause_id=f"refund:{event.refundId}",
                        occurred_at=event.occurredAt,
                        user_id=order.user_id,
                        order_id=order.id,
                    ),
                )
                add_outbox_event(
                    session,
                    order_canceled_notification_event(
                        canceled_order,
                        event.occurredAt,
                    ),
                )
                await session.commit()
                return True
            case (
                OrderStatus.PENDING_PAYMENT
                | OrderStatus.CONFIRMED
                | OrderStatus.PAYMENT_FAILED
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
                await session.commit()
                return False
            case unreachable:
                assert_never(unreachable)


async def apply_refund_failed(
    session_factory: async_sessionmaker[AsyncSession],
    event: RefundFailedEvent,
) -> bool:
    if not refund_event_is_valid(event):
        return False
    async with session_factory() as session:
        order = await _locked_order(session, OrderId(event.orderId))
        if order is None:
            return False
        cancellation = await _locked_cancellation(session, order.id)
        if cancellation is None or not _refund_matches(event, cancellation, order):
            await session.commit()
            return False
        if not await record_processed_event(session, event):
            return False
        match OrderStatus(order.status):
            case OrderStatus.CANCEL_PENDING:
                match RefundStatus(cancellation.refund_status):
                    case RefundStatus.REQUESTED | RefundStatus.PROCESSING:
                        cancellation.refund_status = RefundStatus.FAILED.value
                        cancellation.updated_at = event.occurredAt
                        add_outbox_event(
                            session,
                            refund_failed_notification_event(
                                order_from_record(order),
                                event.occurredAt,
                            ),
                        )
                        await session.commit()
                        return True
                    case RefundStatus.FAILED | RefundStatus.COMPLETED:
                        await session.commit()
                        return False
                    case unreachable_refund_status:
                        assert_never(unreachable_refund_status)
            case (
                OrderStatus.PENDING_PAYMENT
                | OrderStatus.CONFIRMED
                | OrderStatus.PAYMENT_FAILED
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
                await session.commit()
                return False
            case unreachable_order_status:
                assert_never(unreachable_order_status)


async def _locked_order(session: AsyncSession, order_id: OrderId) -> OrderRecord | None:
    return (
        await session.execute(
            select(OrderRecord).where(OrderRecord.id == order_id).with_for_update(),
        )
    ).scalar_one_or_none()


async def _locked_cancellation(
    session: AsyncSession,
    order_id: str,
) -> CancellationRequestRecord | None:
    return (
        await session.execute(
            select(CancellationRequestRecord)
            .where(CancellationRequestRecord.order_id == order_id)
            .with_for_update(),
        )
    ).scalar_one_or_none()


async def _locked_inventory(
    session: AsyncSession,
    order: OrderRecord,
) -> InventoryItemRecord:
    return (
        await session.execute(
            select(InventoryItemRecord)
            .where(
                InventoryItemRecord.drop_id == order.drop_id,
                InventoryItemRecord.product_id == order.product_id,
            )
            .with_for_update(),
        )
    ).scalar_one()


def _refund_matches(
    event: RefundCompletedEvent | RefundFailedEvent,
    cancellation: CancellationRequestRecord,
    order: OrderRecord,
) -> bool:
    return (
        event.refundId == cancellation.id
        and event.producer == "payment-service"
        and event.orderId == order.id
        and event.paymentId == order.payment_id
        and event.userId == order.user_id
        and event.amount == order.amount
        and event.sourceId == cancellation.id
        and event.occurredAt.utcoffset() is not None
    )
