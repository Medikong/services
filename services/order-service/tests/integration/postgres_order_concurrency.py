import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Final, assert_never
from uuid import uuid4

import anyio
import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from app.catalog import ProductForSale
from app.models import DropId, IdempotencyKey, OrderStatus, ProductId, UserId
from app.postgres import Base, PostgresOrderRepository
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    ProductSoldOut,
    ProductUnavailable,
)

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"
ACTIVE_ORDER_STATUSES: Final = (
    OrderStatus.PENDING_PAYMENT.value,
    OrderStatus.CONFIRMED.value,
)


@pytest.mark.anyio
async def test_create_order_reserves_single_remaining_unit_when_two_sessions_race() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    schema_name = f"order_concurrency_{uuid4().hex}"
    product = ProductForSale(
        drop_id=DropId("drop-concurrency"),
        product_id=ProductId("product-concurrency"),
        unit_price=50000,
        remaining_quantity=1,
    )
    commands = (
        CreateOrderCommand(
            user_id=UserId("user-concurrency-001"),
            drop_id=product.drop_id,
            product_id=product.product_id,
            quantity=1,
            idempotency_key=IdempotencyKey("order-concurrency-001"),
        ),
        CreateOrderCommand(
            user_id=UserId("user-concurrency-002"),
            drop_id=product.drop_id,
            product_id=product.product_id,
            quantity=1,
            idempotency_key=IdempotencyKey("order-concurrency-002"),
        ),
    )

    async with _postgres_schema(database_url, schema_name) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_schema_tables(engine)

        # When
        results = await _reserve_concurrently(
            session_factory=session_factory,
            product=product,
            commands=commands,
        )

        # Then
        created_count = 0
        sold_out_count = 0
        for result in results:
            match result:
                case OrderCreated():
                    created_count += 1
                case ProductSoldOut():
                    sold_out_count += 1
                case OrderAlreadyCreated() | OrderIdempotencyConflict() | ProductUnavailable():
                    pytest.fail(f"unexpected reservation result: {type(result).__name__}")
                case unreachable:
                    assert_never(unreachable)

        assert created_count == 1
        assert sold_out_count == 1
        assert await _active_order_count(session_factory, product) == 1
        assert await _reserved_quantity(session_factory, product) == 1


@asynccontextmanager
async def _postgres_schema(
    database_url: str,
    schema_name: str,
) -> AsyncIterator[AsyncEngine]:
    admin_engine = create_async_engine(database_url)
    engine = create_async_engine(
        database_url,
        connect_args={"server_settings": {"search_path": schema_name}},
    )
    try:
        async with admin_engine.begin() as connection:
            await connection.execute(text(f"CREATE SCHEMA {schema_name}"))
        yield engine
    finally:
        await engine.dispose()
        async with admin_engine.begin() as connection:
            await connection.execute(text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE"))
        await admin_engine.dispose()


async def _create_schema_tables(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)


async def _reserve_concurrently(
    *,
    session_factory: async_sessionmaker[AsyncSession],
    product: ProductForSale,
    commands: tuple[CreateOrderCommand, CreateOrderCommand],
) -> list[CreateOrderResult]:
    results: list[CreateOrderResult] = []
    start = anyio.Event()

    async def reserve(command: CreateOrderCommand) -> None:
        repository = PostgresOrderRepository(session_factory, catalog=(product,))
        await start.wait()
        results.append(await repository.create_order(command))

    async with anyio.create_task_group() as task_group:
        for command in commands:
            task_group.start_soon(reserve, command)
        start.set()

    return results


async def _active_order_count(
    session_factory: async_sessionmaker[AsyncSession],
    product: ProductForSale,
) -> int:
    async with session_factory() as session:
        result = await session.execute(
            text(
                """
                SELECT count(*)
                FROM orders
                WHERE drop_id = :drop_id
                  AND product_id = :product_id
                  AND status = ANY(:statuses)
                """,
            ),
            {
                "drop_id": product.drop_id,
                "product_id": product.product_id,
                "statuses": list(ACTIVE_ORDER_STATUSES),
            },
        )
        return int(result.scalar_one())


async def _reserved_quantity(
    session_factory: async_sessionmaker[AsyncSession],
    product: ProductForSale,
) -> int:
    async with session_factory() as session:
        result = await session.execute(
            text(
                """
                SELECT coalesce(sum(quantity), 0)
                FROM orders
                WHERE drop_id = :drop_id
                  AND product_id = :product_id
                  AND status = ANY(:statuses)
                """,
            ),
            {
                "drop_id": product.drop_id,
                "product_id": product.product_id,
                "statuses": list(ACTIVE_ORDER_STATUSES),
            },
        )
        return int(result.scalar_one())
