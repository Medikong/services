import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from typing import Final
from uuid import uuid4

from contracts import PaymentApprovedEvent, PaymentFailedEvent, RefundCompletedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from app.catalog import ProductForSale
from app.models import DropId, IdempotencyKey, ProductId, UserId
from app.postgres import Base, PostgresOrderRepository
from app.store import CreateOrderCommand

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"
OCCURRED_AT: Final = datetime(2026, 7, 14, 12, 0, tzinfo=UTC)


@asynccontextmanager
async def inventory_repository(
    product: ProductForSale,
) -> AsyncIterator[tuple[PostgresOrderRepository, async_sessionmaker[AsyncSession]]]:
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await seed_inventory(session_factory, product)
        yield (
            PostgresOrderRepository(session_factory, catalog=(product,)),
            session_factory,
        )


@asynccontextmanager
async def postgres_schema(database_url: str) -> AsyncIterator[AsyncEngine]:
    schema_name = f"inventory_lifecycle_{uuid4().hex}"
    admin_engine = create_async_engine(database_url)
    engine = create_async_engine(
        database_url,
        connect_args={"server_settings": {"search_path": schema_name}},
    )
    try:
        async with admin_engine.begin() as connection:
            await connection.execute(text(f"CREATE SCHEMA {schema_name}"))
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)
        yield engine
    finally:
        await engine.dispose()
        async with admin_engine.begin() as connection:
            await connection.execute(
                text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE")
            )
        await admin_engine.dispose()


async def seed_inventory(
    session_factory: async_sessionmaker[AsyncSession], product: ProductForSale
) -> None:
    async with session_factory.begin() as session:
        await session.execute(
            text(
                "INSERT INTO inventory_items VALUES "
                "(:drop_id, :product_id, 42, 0, 0, 0)"
            ),
            {"drop_id": product.drop_id, "product_id": product.product_id},
        )


async def inventory_state(
    session_factory: async_sessionmaker[AsyncSession], product: ProductForSale
) -> tuple[int, int, int, int]:
    async with session_factory() as session:
        row = (
            await session.execute(
                text(
                    "SELECT total_quantity, reserved_quantity, sold_quantity, version "
                    "FROM inventory_items WHERE drop_id=:drop_id AND product_id=:product_id"
                ),
                {"drop_id": product.drop_id, "product_id": product.product_id},
            )
        ).one()
    return tuple(row)


async def inventory_outbox(
    session_factory: async_sessionmaker[AsyncSession], product: ProductForSale
) -> list[tuple[int, int, int, int, int]]:
    async with session_factory() as session:
        rows = (
            await session.execute(
                text(
                    "SELECT (payload->>'totalQuantity')::int, "
                    "(payload->>'reservedQuantity')::int, "
                    "(payload->>'soldQuantity')::int, "
                    "(payload->>'remainingQuantity')::int, "
                    "(payload->>'inventoryVersion')::int "
                    "FROM outbox_events WHERE event_type='inventory.changed' "
                    "AND aggregate_id=:aggregate_id "
                    "ORDER BY (payload->>'inventoryVersion')::int"
                ),
                {"aggregate_id": f"{product.drop_id}:{product.product_id}"},
            )
        ).all()
    return [tuple(row) for row in rows]


async def inbox_count(session_factory: async_sessionmaker[AsyncSession]) -> int:
    async with session_factory() as session:
        return int(
            (
                await session.execute(text("SELECT count(*) FROM processed_events"))
            ).scalar_one()
        )


async def outbox_types(
    session_factory: async_sessionmaker[AsyncSession],
) -> list[str]:
    async with session_factory() as session:
        rows = (
            await session.execute(
                text(
                    "SELECT event_type FROM outbox_events ORDER BY occurred_at, event_id"
                )
            )
        ).scalars()
    return list(rows)


async def mark_cancel_pending(
    session_factory: async_sessionmaker[AsyncSession], order_id: str
) -> None:
    async with session_factory.begin() as session:
        await session.execute(
            text("UPDATE orders SET status='CANCEL_PENDING' WHERE id=:order_id"),
            {"order_id": order_id},
        )


async def order_status(
    session_factory: async_sessionmaker[AsyncSession], order_id: str
) -> str:
    async with session_factory() as session:
        return str(
            (
                await session.execute(
                    text("SELECT status FROM orders WHERE id=:order_id"),
                    {"order_id": order_id},
                )
            ).scalar_one()
        )


def product(suffix: str) -> ProductForSale:
    return ProductForSale(
        drop_id=DropId(f"drop-{suffix}"),
        product_id=ProductId(f"product-{suffix}"),
        unit_price=50000,
    )


def command(product_for_sale: ProductForSale, suffix: str) -> CreateOrderCommand:
    return CreateOrderCommand(
        user_id=UserId(f"user-{suffix}"),
        drop_id=product_for_sale.drop_id,
        product_id=product_for_sale.product_id,
        quantity=10,
        idempotency_key=IdempotencyKey(f"order-{suffix}"),
    )


def approved(order_id: str, user_id: str, amount: int) -> PaymentApprovedEvent:
    payment_id = f"payment-{order_id}"
    return PaymentApprovedEvent(
        eventId=f"evt-approved-{order_id}",
        userId=user_id,
        sourceId=payment_id,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        orderId=order_id,
        paymentId=payment_id,
        amount=amount,
    )


def failed(
    order_id: str,
    user_id: str,
    amount: int,
    suffix: str,
) -> PaymentFailedEvent:
    payment_id = f"payment-{suffix}"
    return PaymentFailedEvent(
        eventId=f"evt-failed-{suffix}-{order_id}",
        userId=user_id,
        sourceId=payment_id,
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        orderId=order_id,
        paymentId=payment_id,
        amount=amount,
        reason="declined",
    )


def refund(order_id: str, refund_id: str, event_id: str) -> RefundCompletedEvent:
    return RefundCompletedEvent(
        eventId=event_id,
        userId="user",
        sourceId="payment",
        occurredAt=OCCURRED_AT,
        producer="payment-service",
        refundId=refund_id,
        orderId=order_id,
        paymentId=f"payment-{order_id}",
        amount=500000,
    )
