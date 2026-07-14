from dataclasses import dataclass
from datetime import datetime
from typing import assert_never

from contracts import PaymentApprovedEvent, PaymentFailedEvent, RefundCompletedEvent
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.events import inventory_changed_event, order_expired_event
from app.models import OrderId, OrderStatus
from app.outbox import add_outbox_event
from app.postgres_inbox import record_processed_event
from app.postgres_mapping import order_from_record
from app.records import InventoryItemRecord, OrderRecord


@dataclass(frozen=True, slots=True)
class InventoryItemMissingError(RuntimeError):
    drop_id: str
    product_id: str

    def __str__(self) -> str:
        return f"inventory item missing for {self.drop_id}/{self.product_id}"


async def sell_reserved_inventory(
    session: AsyncSession,
    order: OrderRecord,
    event: PaymentApprovedEvent,
) -> None:
    inventory = await _locked_inventory(session, order)
    inventory.reserved_quantity -= order.quantity
    inventory.sold_quantity += order.quantity
    inventory.version += 1
    add_outbox_event(
        session,
        inventory_changed_event(
            inventory,
            cause_id=f"approve:{event.eventId}",
            occurred_at=event.occurredAt,
            user_id=order.user_id,
            order_id=order.id,
        ),
    )


async def release_reserved_inventory(
    session: AsyncSession,
    order: OrderRecord,
    event: PaymentFailedEvent,
) -> None:
    inventory = await _locked_inventory(session, order)
    inventory.reserved_quantity -= order.quantity
    inventory.version += 1
    add_outbox_event(
        session,
        inventory_changed_event(
            inventory,
            cause_id=f"failure:{event.eventId}",
            occurred_at=event.occurredAt,
            user_id=order.user_id,
            order_id=order.id,
        ),
    )


async def expire_pending_order(
    session_factory: async_sessionmaker[AsyncSession],
    order_id: OrderId,
    occurred_at: datetime,
) -> bool:
    async with session_factory() as session:
        order = await _locked_order(session, order_id)
        if (
            order is None
            or OrderStatus(order.status) is not OrderStatus.PENDING_PAYMENT
        ):
            return False
        inventory = await _locked_inventory(session, order)
        inventory.reserved_quantity -= order.quantity
        inventory.version += 1
        order.status = OrderStatus.EXPIRED.value
        expired_order = order_from_record(order)
        add_outbox_event(
            session,
            inventory_changed_event(
                inventory,
                cause_id=f"expire:{order.id}",
                occurred_at=occurred_at,
                user_id=order.user_id,
                order_id=order.id,
            ),
        )
        add_outbox_event(session, order_expired_event(expired_order, occurred_at))
        await session.commit()
        return True


async def apply_refund_completed(
    session_factory: async_sessionmaker[AsyncSession],
    event: RefundCompletedEvent,
) -> bool:
    async with session_factory() as session:
        order = await _locked_order(session, OrderId(event.orderId))
        if order is None:
            await record_processed_event(session, event)
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


async def _locked_order(session: AsyncSession, order_id: OrderId) -> OrderRecord | None:
    result = await session.execute(
        select(OrderRecord).where(OrderRecord.id == order_id).with_for_update(),
    )
    return result.scalar_one_or_none()


async def _locked_inventory(
    session: AsyncSession, order: OrderRecord
) -> InventoryItemRecord:
    result = await session.execute(
        select(InventoryItemRecord)
        .where(
            InventoryItemRecord.drop_id == order.drop_id,
            InventoryItemRecord.product_id == order.product_id,
        )
        .with_for_update(),
    )
    inventory = result.scalar_one_or_none()
    if inventory is None:
        raise InventoryItemMissingError(
            drop_id=order.drop_id,
            product_id=order.product_id,
        )
    return inventory
