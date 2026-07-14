from __future__ import annotations

import asyncpg


async def create_current_schema_with_rows(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            CREATE TABLE orders (
                id VARCHAR(64) PRIMARY KEY,
                user_id VARCHAR(64) NOT NULL,
                drop_id VARCHAR(64) NOT NULL,
                product_id VARCHAR(64) NOT NULL,
                quantity INTEGER NOT NULL,
                amount INTEGER NOT NULL,
                status VARCHAR(32) NOT NULL,
                idempotency_key VARCHAR(128) NOT NULL,
                payment_id VARCHAR(64),
                created_at TIMESTAMPTZ NOT NULL,
                confirmed_at TIMESTAMPTZ,
                CONSTRAINT uq_orders_user_idempotency_key
                    UNIQUE (user_id, idempotency_key)
            );
            CREATE INDEX ix_orders_user_status ON orders (user_id, status);
            CREATE INDEX ix_orders_product_status
                ON orders (drop_id, product_id, status);
            CREATE TABLE processed_payment_events (
                event_id VARCHAR(128) PRIMARY KEY,
                event_type VARCHAR(32) NOT NULL,
                order_id VARCHAR(64) NOT NULL,
                payment_id VARCHAR(64) NOT NULL,
                processed_at TIMESTAMPTZ NOT NULL
            );
            INSERT INTO orders (
                id, user_id, drop_id, product_id, quantity, amount, status,
                idempotency_key, payment_id, created_at, confirmed_at
            ) VALUES (
                'order-legacy-001', 'user-legacy-001', 'drop-001',
                'product-001', 1, 50000, 'CONFIRMED', 'legacy-key-001',
                'payment-legacy-001', '2026-07-01T00:00:00Z',
                '2026-07-01T00:01:00Z'
            );
            INSERT INTO processed_payment_events (
                event_id, event_type, order_id, payment_id, processed_at
            ) VALUES (
                'evt-payment-failed-legacy-001', 'PAYMENT_FAILED',
                'order-legacy-001', 'payment-legacy-001',
                '2026-07-01T00:02:00Z'
            );
            """,
        )
    finally:
        await connection.close()


async def read_preserved_rows(database_url: str) -> tuple[str, str, str, str]:
    connection = await asyncpg.connect(database_url)
    try:
        order = await connection.fetchrow(
            """
            SELECT id, status, fulfillment_status FROM orders
            WHERE id = 'order-legacy-001'
            """,
        )
        event_id = await connection.fetchval(
            """
            SELECT event_id FROM processed_payment_events
            WHERE event_id = 'evt-payment-failed-legacy-001'
            """,
        )
    finally:
        await connection.close()
    assert order is not None
    assert event_id is not None
    return order["id"], order["status"], order["fulfillment_status"], event_id


async def schema_state(database_url: str) -> tuple[tuple[str, ...], str, int]:
    connection = await asyncpg.connect(database_url)
    try:
        tables = await connection.fetch(
            """
            SELECT table_name FROM information_schema.tables
            WHERE table_schema = 'public' ORDER BY table_name
            """,
        )
        revision = await connection.fetchval("SELECT version_num FROM alembic_version")
        inventory_count = await connection.fetchval(
            "SELECT count(*) FROM inventory_items"
        )
    finally:
        await connection.close()
    assert revision is not None
    return tuple(row["table_name"] for row in tables), revision, inventory_count


async def order_columns(database_url: str) -> set[str]:
    connection = await asyncpg.connect(database_url)
    try:
        rows = await connection.fetch(
            """
            SELECT column_name FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'orders'
            """,
        )
    finally:
        await connection.close()
    return {row["column_name"] for row in rows}


async def add_contradictory_order(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            INSERT INTO orders (
                id, user_id, drop_id, product_id, quantity, amount, status,
                idempotency_key, created_at
            ) VALUES (
                'order-legacy-contradiction', 'user-legacy-002', 'drop-001',
                'product-001', 42, 2100000, 'PENDING_PAYMENT',
                'legacy-key-002', '2026-07-01T00:03:00Z'
            )
            """,
        )
    finally:
        await connection.close()


async def repair_contradictory_order(database_url: str) -> int:
    connection = await asyncpg.connect(database_url)
    try:
        row_count = await connection.fetchval("SELECT count(*) FROM orders")
        await connection.execute(
            """
            UPDATE orders SET quantity = 40, amount = 2000000
            WHERE id = 'order-legacy-contradiction'
            """,
        )
    finally:
        await connection.close()
    return row_count


async def insert_invalid_cancellation_request(
    database_url: str,
    order_id: str,
) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            INSERT INTO cancellation_requests (
                id, order_id, user_id, idempotency_key, reason,
                refund_status, created_at, updated_at
            ) VALUES (
                'cancellation-invalid-status', $1, 'user-legacy-001',
                'cancel-invalid-status', 'customer request', 'BOGUS',
                '2026-07-01T01:00:00Z', '2026-07-01T01:00:00Z'
            )
            """,
            order_id,
        )
    finally:
        await connection.close()


async def add_fresh_order(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            INSERT INTO orders (
                id, user_id, drop_id, product_id, quantity, amount, status,
                idempotency_key, created_at, fulfillment_status
            ) VALUES (
                'order-fresh-001', 'user-legacy-001', 'drop-001',
                'product-001', 1, 50000, 'CONFIRMED', 'fresh-order-key',
                '2026-07-01T00:00:00Z', 'NOT_STARTED'
            )
            """,
        )
    finally:
        await connection.close()
