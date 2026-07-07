from datetime import UTC, datetime
from hashlib import blake2b
from typing import Final, assert_never
from uuid import uuid4

from contracts import PaymentApprovedEvent, PaymentFailedEvent
from sqlalchemy import DateTime, Index, Integer, String, UniqueConstraint, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

from app.catalog import PRODUCT_CATALOG, ProductForSale, product_for
from app.models import Order, OrderId, OrderStatus, PaymentId
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentApprovalResult,
    PaymentEventOrderMissing,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentFailureResult,
    PaymentIgnored,
    ProductUnavailable,
    order_matches_command,
)

RESERVED_ORDER_STATUSES: Final = (
    OrderStatus.PENDING_PAYMENT.value,
    OrderStatus.CONFIRMED.value,
)


class Base(DeclarativeBase):
    pass


class OrderRecord(Base):
    __tablename__ = "orders"
    __table_args__ = (
        UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_orders_user_idempotency_key",
        ),
        Index("ix_orders_user_status", "user_id", "status"),
        Index("ix_orders_product_status", "drop_id", "product_id", "status"),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    drop_id: Mapped[str] = mapped_column(String(64), nullable=False)
    product_id: Mapped[str] = mapped_column(String(64), nullable=False)
    quantity: Mapped[int] = mapped_column(Integer, nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    idempotency_key: Mapped[str] = mapped_column(String(128), nullable=False)
    payment_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    confirmed_at: Mapped[datetime | None] = mapped_column(
        DateTime(timezone=True),
        nullable=True,
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
                return ProductUnavailable(
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
                order=_order_from_record(record),
                idempotency_key=command.idempotency_key,
            )

    async def get_order(self, order_id: OrderId) -> Order | None:
        async with self._session_factory() as session:
            record = await session.get(OrderRecord, order_id)
            if record is None:
                return None
            return _order_from_record(record)

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult:
        order_id = OrderId(event.orderId)
        async with self._session_factory() as session:
            record = await session.get(OrderRecord, order_id)
            if record is None:
                return PaymentEventOrderMissing(order_id=order_id)

            status = OrderStatus(record.status)
            match status:
                case OrderStatus.PENDING_PAYMENT:
                    record.status = OrderStatus.CONFIRMED.value
                    record.payment_id = PaymentId(event.paymentId)
                    record.confirmed_at = event.occurredAt
                    await session.commit()
                    return PaymentApplied(order=_order_from_record(record))
                case OrderStatus.CONFIRMED:
                    return PaymentAlreadyApplied(order=_order_from_record(record))
                case OrderStatus.PAYMENT_FAILED | OrderStatus.CANCELED | OrderStatus.EXPIRED:
                    return PaymentIgnored(order=_order_from_record(record))
                case unreachable:
                    assert_never(unreachable)

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult:
        order_id = OrderId(event.orderId)
        async with self._session_factory() as session:
            record = await session.get(OrderRecord, order_id)
            if record is None:
                return PaymentEventOrderMissing(order_id=order_id)

            status = OrderStatus(record.status)
            match status:
                case OrderStatus.PENDING_PAYMENT:
                    record.status = OrderStatus.PAYMENT_FAILED.value
                    record.payment_id = PaymentId(event.paymentId)
                    await session.commit()
                    return PaymentFailureApplied(order=_order_from_record(record))
                case OrderStatus.PAYMENT_FAILED:
                    return PaymentFailureAlreadyApplied(order=_order_from_record(record))
                case OrderStatus.CONFIRMED | OrderStatus.CANCELED | OrderStatus.EXPIRED:
                    return PaymentIgnored(order=_order_from_record(record))
                case unreachable:
                    assert_never(unreachable)

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
        return _order_from_record(record)


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


def _order_from_record(record: OrderRecord) -> Order:
    return Order(
        id=record.id,
        userId=record.user_id,
        dropId=record.drop_id,
        productId=record.product_id,
        quantity=record.quantity,
        amount=record.amount,
        status=OrderStatus(record.status),
        paymentId=record.payment_id,
        createdAt=record.created_at,
        confirmedAt=record.confirmed_at,
    )
