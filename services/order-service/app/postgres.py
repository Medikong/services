from datetime import UTC, datetime
from hashlib import blake2b
from typing import Final
from uuid import uuid4

from contracts import PaymentApprovedEvent, PaymentFailedEvent
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.catalog import PRODUCT_CATALOG, ProductForSale, product_for
from app.events import order_created_event
from app.models import Order, OrderId, OrderStatus
from app.outbox import add_outbox_event
from app.postgres_mapping import order_from_record
from app.postgres_payments import apply_payment_approved, apply_payment_failed
from app.records import Base as Base
from app.records import OrderRecord
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    PaymentApprovalResult,
    PaymentFailureResult,
    ProductSoldOut,
    ProductUnavailable,
    order_matches_command,
)

RESERVED_ORDER_STATUSES: Final = (
    OrderStatus.PENDING_PAYMENT.value,
    OrderStatus.CONFIRMED.value,
)


class PostgresOrderRepository:
    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        catalog: tuple[ProductForSale, ...] = PRODUCT_CATALOG,
    ) -> None:
        self._session_factory = session_factory
        self._catalog = catalog

    async def create_order(self, command: CreateOrderCommand) -> CreateOrderResult:
        async with self._session_factory() as session:
            replayed_order = await self._replayed_order(session, command)
            if replayed_order is not None:
                if not order_matches_command(replayed_order, command):
                    return OrderIdempotencyConflict(order=replayed_order)
                return OrderAlreadyCreated(order=replayed_order)

            product = product_for(self._catalog, command.drop_id, command.product_id)
            if product is None:
                return ProductUnavailable(
                    drop_id=command.drop_id,
                    product_id=command.product_id,
                )

            await _lock_product_inventory(session, product)
            replayed_order = await self._replayed_order(session, command)
            if replayed_order is not None:
                if not order_matches_command(replayed_order, command):
                    return OrderIdempotencyConflict(order=replayed_order)
                return OrderAlreadyCreated(order=replayed_order)

            reserved_quantity = await _reserved_quantity(session, product)
            if command.quantity > product.remaining_quantity - reserved_quantity:
                return ProductSoldOut(
                    drop_id=command.drop_id,
                    product_id=command.product_id,
                )

            record = OrderRecord(
                id=_new_order_id(),
                user_id=command.user_id,
                drop_id=command.drop_id,
                product_id=command.product_id,
                quantity=command.quantity,
                amount=product.unit_price * command.quantity,
                status=OrderStatus.PENDING_PAYMENT.value,
                idempotency_key=command.idempotency_key,
                created_at=datetime.now(UTC),
            )
            session.add(record)
            add_outbox_event(
                session,
                order_created_event(order_from_record(record), command.idempotency_key),
            )
            try:
                await session.commit()
            except IntegrityError:
                await session.rollback()
                replayed_after_conflict = await self._replayed_order(session, command)
                if replayed_after_conflict is not None:
                    if not order_matches_command(replayed_after_conflict, command):
                        return OrderIdempotencyConflict(order=replayed_after_conflict)
                    return OrderAlreadyCreated(order=replayed_after_conflict)
                raise
            return OrderCreated(
                order=order_from_record(record),
                idempotency_key=command.idempotency_key,
            )

    async def get_order(self, order_id: OrderId) -> Order | None:
        async with self._session_factory() as session:
            record = await session.get(OrderRecord, order_id)
            if record is None:
                return None
            return order_from_record(record)

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult:
        return await apply_payment_approved(self._session_factory, event)

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult:
        return await apply_payment_failed(self._session_factory, event)

    async def _replayed_order(
        self,
        session: AsyncSession,
        command: CreateOrderCommand,
    ) -> Order | None:
        result = await session.execute(
            select(OrderRecord).where(
                OrderRecord.user_id == command.user_id,
                OrderRecord.idempotency_key == command.idempotency_key,
            ),
        )
        record = result.scalar_one_or_none()
        if record is None:
            return None
        return order_from_record(record)


async def _lock_product_inventory(
    session: AsyncSession,
    product: ProductForSale,
) -> None:
    await session.execute(
        select(func.pg_advisory_xact_lock(_inventory_lock_key(product))),
    )


async def _reserved_quantity(
    session: AsyncSession,
    product: ProductForSale,
) -> int:
    result = await session.execute(
        select(func.coalesce(func.sum(OrderRecord.quantity), 0)).where(
            OrderRecord.drop_id == product.drop_id,
            OrderRecord.product_id == product.product_id,
            OrderRecord.status.in_(RESERVED_ORDER_STATUSES),
        ),
    )
    return int(result.scalar_one())


def _inventory_lock_key(product: ProductForSale) -> int:
    lock_source = f"{product.drop_id}\0{product.product_id}".encode("utf-8")
    digest = blake2b(lock_source, digest_size=8).digest()
    return int.from_bytes(digest, byteorder="big", signed=True)


def _new_order_id() -> OrderId:
    return OrderId(f"order-{uuid4().hex[:12]}")
