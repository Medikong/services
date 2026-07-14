import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from typing import Final, assert_never
from uuid import uuid4

import anyio
import pytest
from contracts import PaymentFailedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from app.catalog import ProductForSale
from app.models import DropId, IdempotencyKey, OrderId, OrderStatus, ProductId, UserId
from app.postgres import Base, PostgresOrderRepository
from app.store import (
    CreateOrderCommand,
    OrderCreated,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentFailureResult,
    PaymentIgnored,
    PaymentEventOrderMissing,
)

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"
PROCESSED_PAYMENT_FAILED_TYPE: Final = "payment.failed"


@pytest.mark.anyio
async def test_payment_failed_records_inbox_once_when_duplicate_event_races() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    schema_name = f"payment_failure_idempotency_{uuid4().hex}"
    product = ProductForSale(
        drop_id=DropId("drop-payment-failure-idempotency"),
        product_id=ProductId("product-payment-failure-idempotency"),
        unit_price=50000,
        remaining_quantity=2,
    )
    command = CreateOrderCommand(
        user_id=UserId("user-payment-failure-idempotency"),
        drop_id=product.drop_id,
        product_id=product.product_id,
        quantity=1,
        idempotency_key=IdempotencyKey("order-payment-failure-idempotency"),
    )

    async with _postgres_schema(database_url, schema_name) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_schema_tables(engine)
        order_id = await _create_pending_order(session_factory, product, command)
        event = PaymentFailedEvent(
            eventId="evt-payment-failed-duplicate",
            userId=command.user_id,
            sourceId="payment-service",
            occurredAt=datetime.now(UTC),
            producer="payment-service",
            orderId=order_id,
            paymentId="payment-duplicate",
            amount=product.unit_price * command.quantity,
            reason="duplicate delivery",
        )

        # When
        results = await _fail_concurrently(session_factory, event)

        # Then
        applied_count = 0
        already_applied_count = 0
        for result in results:
            match result:
                case PaymentFailureApplied():
                    applied_count += 1
                case PaymentFailureAlreadyApplied():
                    already_applied_count += 1
                case PaymentEventOrderMissing() | PaymentIgnored():
                    pytest.fail(
                        f"unexpected payment failure result: {type(result).__name__}"
                    )
                case unreachable:
                    assert_never(unreachable)

        assert applied_count == 1
        assert already_applied_count == 1
        assert await _processed_failure_rows(session_factory, event.eventId) == [
            (
                event.eventId,
                PROCESSED_PAYMENT_FAILED_TYPE,
                "order",
                event.orderId,
            ),
        ]
        assert await _persisted_order_failure_state(session_factory, order_id) == (
            OrderStatus.PAYMENT_FAILED.value,
            event.paymentId,
        )


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
            await connection.execute(
                text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE")
            )
        await admin_engine.dispose()


async def _create_schema_tables(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)


async def _create_pending_order(
    session_factory: async_sessionmaker[AsyncSession],
    product: ProductForSale,
    command: CreateOrderCommand,
) -> OrderId:
    repository = PostgresOrderRepository(session_factory, catalog=(product,))
    result = await repository.create_order(command)
    match result:
        case OrderCreated(order=order):
            return OrderId(order.id)
        case unreachable:
            assert_never(unreachable)


async def _fail_concurrently(
    session_factory: async_sessionmaker[AsyncSession],
    event: PaymentFailedEvent,
) -> list[PaymentFailureResult]:
    results: list[PaymentFailureResult] = []
    start = anyio.Event()

    async def fail_payment() -> None:
        repository = PostgresOrderRepository(session_factory)
        await start.wait()
        results.append(await repository.apply_payment_failed(event))

    async with anyio.create_task_group() as task_group:
        task_group.start_soon(fail_payment)
        task_group.start_soon(fail_payment)
        start.set()

    return results


async def _processed_failure_rows(
    session_factory: async_sessionmaker[AsyncSession],
    event_id: str,
) -> list[tuple[str, str, str, str]]:
    async with session_factory() as session:
        result = await session.execute(
            text(
                """
                SELECT event_id, event_type, aggregate_type, aggregate_id
                FROM processed_events
                WHERE event_id = :event_id
                """,
            ),
            {"event_id": event_id},
        )
        return [
            (row.event_id, row.event_type, row.aggregate_type, row.aggregate_id)
            for row in result
        ]


async def _persisted_order_failure_state(
    session_factory: async_sessionmaker[AsyncSession],
    order_id: OrderId,
) -> tuple[str, str | None]:
    async with session_factory() as session:
        result = await session.execute(
            text(
                """
                SELECT status, payment_id
                FROM orders
                WHERE id = :order_id
                """,
            ),
            {"order_id": order_id},
        )
        row = result.one()
        return (row.status, row.payment_id)
