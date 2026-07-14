import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Final
from uuid import uuid4

import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine

from app.postgres import Base

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


@pytest.mark.anyio
async def test_current_schema_preserves_known_orders_and_terminal_payments() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    schema_name = f"payment_characterization_{uuid4().hex}"

    async with _postgres_schema(database_url, schema_name) as engine:
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)
            await connection.execute(
                text(
                    """
                    INSERT INTO known_orders (order_id, user_id, amount, created_at)
                    VALUES ('order-approved', 'user-001', 50000, now()),
                           ('order-failed', 'user-002', 30000, now())
                    """,
                ),
            )
            await connection.execute(
                text(
                    """
                    INSERT INTO payments (
                        id, order_id, user_id, amount, method, status,
                        idempotency_key, created_at, approved_at, failed_at,
                        failure_reason
                    ) VALUES
                    (
                        'payment-approved', 'order-approved', 'user-001', 50000,
                        'MOCK_CARD', 'APPROVED', 'approve-key', now(), now(),
                        NULL, NULL
                    ),
                    (
                        'payment-failed', 'order-failed', 'user-002', 30000,
                        'MOCK_CARD', 'FAILED', 'fail-key', now(), NULL, now(),
                        'card_declined'
                    )
                    """,
                ),
            )

        # When
        async with engine.connect() as connection:
            rows = (
                (
                    await connection.execute(
                        text(
                            """
                        SELECT id, order_id, status
                        FROM payments
                        ORDER BY id
                        """,
                        ),
                    )
                )
                .tuples()
                .all()
            )

        # Then
        assert rows == [
            ("payment-approved", "order-approved", "APPROVED"),
            ("payment-failed", "order-failed", "FAILED"),
        ]


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
                text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE"),
            )
        await admin_engine.dispose()
