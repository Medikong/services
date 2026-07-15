from __future__ import annotations

import os

import anyio
import asyncpg
import pytest
from sqlalchemy.ext.asyncio import create_async_engine

from app.schema import database_migration_is_current
from tests.migration_cli import reset_database, run_migration

TEST_DATABASE_ENV = "TEST_DATABASE_URL"


async def _insert_legacy_pending_order(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            INSERT INTO orders (
                id, user_id, drop_id, product_id, quantity, amount, status,
                idempotency_key, created_at, fulfillment_status, expires_at
            ) VALUES (
                'order-legacy-pending', 'user-legacy', 'drop-001', 'product-001',
                1, 50000, 'PENDING_PAYMENT', 'legacy-pending',
                '2026-07-15T00:00:00Z', 'NOT_STARTED', NULL
            )
            """
        )
    finally:
        await connection.close()


async def _expiry_schema_state(database_url: str) -> tuple[str, str]:
    connection = await asyncpg.connect(database_url)
    try:
        deadline = await connection.fetchval(
            "SELECT expires_at::text FROM orders WHERE id='order-legacy-pending'"
        )
        index_definition = await connection.fetchval(
            "SELECT indexdef FROM pg_indexes "
            "WHERE tablename='orders' AND indexname='ix_orders_pending_expiry'"
        )
        return str(deadline), str(index_definition)
    finally:
        await connection.close()


async def _migration_is_ready(database_url: str) -> bool:
    async_url = database_url.replace("postgresql://", "postgresql+asyncpg://", 1)
    engine = create_async_engine(async_url)
    try:
        return await database_migration_is_current(engine)
    finally:
        await engine.dispose()


async def _create_drifted_expiry_index(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute("CREATE INDEX ix_orders_pending_expiry ON orders (id)")
    finally:
        await connection.close()


def test_expiry_migration_backfills_pending_deadlines_and_adds_due_index() -> None:
    database_url = os.getenv(TEST_DATABASE_ENV)
    if database_url is None:
        pytest.fail(f"{TEST_DATABASE_ENV} is required for migration tests")
    anyio.run(reset_database, database_url)
    try:
        first = run_migration(database_url, "upgrade", "20260714_0001")
        assert first.returncode == 0, first.stderr
        assert anyio.run(_migration_is_ready, database_url) is False
        anyio.run(_insert_legacy_pending_order, database_url)

        second = run_migration(database_url, "upgrade", "head")

        assert second.returncode == 0, second.stderr
        deadline, index_definition = anyio.run(_expiry_schema_state, database_url)
        assert deadline.startswith("2026-07-15 00:05:00")
        assert "(expires_at, id)" in index_definition
        assert "PENDING_PAYMENT" in index_definition
        assert "expires_at IS NOT NULL" in index_definition
        assert anyio.run(_migration_is_ready, database_url) is True
    finally:
        anyio.run(reset_database, database_url)


def test_expiry_migration_rejects_drifted_existing_index() -> None:
    database_url = os.getenv(TEST_DATABASE_ENV)
    if database_url is None:
        pytest.fail(f"{TEST_DATABASE_ENV} is required for migration tests")
    anyio.run(reset_database, database_url)
    try:
        first = run_migration(database_url, "upgrade", "20260714_0001")
        assert first.returncode == 0, first.stderr
        anyio.run(_create_drifted_expiry_index, database_url)

        second = run_migration(database_url, "upgrade", "head")

        assert second.returncode != 0
        assert "ix_orders_pending_expiry definition mismatch" in second.stderr
        assert anyio.run(_migration_is_ready, database_url) is False
    finally:
        anyio.run(reset_database, database_url)
