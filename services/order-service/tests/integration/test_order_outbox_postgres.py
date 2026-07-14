import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from typing import Final
from uuid import uuid4

import pytest
from contracts import PaymentApprovedEvent, PaymentFailedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    async_sessionmaker,
    create_async_engine,
)

from app.models import DropId, IdempotencyKey, ProductId, UserId
from app.postgres import Base, PostgresOrderRepository
from app.store import (
    CreateOrderCommand,
    OrderCreated,
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentEventOrderMissing,
)

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"


@pytest.mark.anyio
async def test_order_and_created_event_commit_in_one_transaction() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        command = _create_command("atomic-create")

        # When
        result = await repository.create_order(command)

        # Then
        assert isinstance(result, OrderCreated)
        async with session_factory() as session:
            row = (
                await session.execute(
                    text(
                        """
                        SELECT o.id, e.event_type, e.topic, e.message_key,
                               e.payload->>'eventId' AS payload_event_id
                        FROM orders AS o
                        JOIN outbox_events AS e ON e.aggregate_id = o.id
                        WHERE o.id = :order_id
                        """,
                    ),
                    {"order_id": result.order.id},
                )
            ).one()
        assert row == (
            result.order.id,
            "order.created",
            "order.created",
            result.order.id,
            row.payload_event_id,
        )
        assert row.payload_event_id


@pytest.mark.anyio
async def test_duplicate_payment_event_changes_order_and_outbox_once() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(_create_command("duplicate-payment"))
        assert isinstance(created, OrderCreated)
        event = PaymentApprovedEvent(
            eventId="evt-payment-approved-duplicate-001",
            userId=created.order.userId,
            sourceId="payment-service",
            occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
            producer="payment-service",
            orderId=created.order.id,
            paymentId="payment-duplicate-001",
            amount=created.order.amount,
        )

        # When
        first_result = await repository.apply_payment_approved(event)
        second_result = await repository.apply_payment_approved(event)

        # Then
        assert isinstance(first_result, PaymentApplied)
        assert isinstance(second_result, PaymentAlreadyApplied)
        async with session_factory() as session:
            persisted = (
                await session.execute(
                    text(
                        """
                        SELECT o.status,
                            (SELECT count(*) FROM processed_events
                             WHERE event_id = :event_id) AS inbox_count,
                            (SELECT count(*) FROM outbox_events
                             WHERE aggregate_id = :order_id
                               AND event_type = 'notification.requested') AS notification_count
                        FROM orders AS o
                        WHERE o.id = :order_id
                        """,
                    ),
                    {"event_id": event.eventId, "order_id": event.orderId},
                )
            ).one()
        assert persisted == ("CONFIRMED", 1, 1)


@pytest.mark.anyio
async def test_missing_order_payment_event_is_recorded_before_it_is_ignored() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        event = PaymentFailedEvent(
            eventId="evt-payment-failed-missing-order-001",
            userId="user-missing-order",
            sourceId="payment-service",
            occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
            producer="payment-service",
            orderId="order-missing",
            paymentId="payment-missing-order",
            amount=50000,
            reason="order_not_projected",
        )

        # When
        result = await repository.apply_payment_failed(event)

        # Then
        assert isinstance(result, PaymentEventOrderMissing)
        async with session_factory() as session:
            inbox_row = (
                await session.execute(
                    text(
                        """
                        SELECT event_id, event_type, aggregate_type, aggregate_id
                        FROM processed_events
                        WHERE event_id = :event_id
                        """,
                    ),
                    {"event_id": event.eventId},
                )
            ).one()
        assert inbox_row == (
            event.eventId,
            "payment.failed",
            "order",
            event.orderId,
        )


@asynccontextmanager
async def _postgres_schema(database_url: str) -> AsyncIterator[AsyncEngine]:
    schema_name = f"order_outbox_{uuid4().hex}"
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


def _create_command(suffix: str) -> CreateOrderCommand:
    return CreateOrderCommand(
        user_id=UserId(f"user-{suffix}"),
        drop_id=DropId("drop-001"),
        product_id=ProductId("product-001"),
        quantity=1,
        idempotency_key=IdempotencyKey(f"order-{suffix}"),
    )
