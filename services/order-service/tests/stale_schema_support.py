from __future__ import annotations

import asyncpg


async def add_partial_processed_events_schema(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            CREATE TABLE processed_events (
                event_id VARCHAR(128) PRIMARY KEY
            );
            INSERT INTO processed_events (event_id)
            VALUES ('evt-partial-legacy-001')
            """,
        )
    finally:
        await connection.close()


async def legacy_source_state(database_url: str) -> tuple[str, str, str, int]:
    connection = await asyncpg.connect(database_url)
    try:
        order = await connection.fetchrow(
            "SELECT id, status FROM orders WHERE id = 'order-legacy-001'",
        )
        payment_event_id = await connection.fetchval(
            """
            SELECT event_id FROM processed_payment_events
            WHERE event_id = 'evt-payment-failed-legacy-001'
            """,
        )
        partial_event_count = await connection.fetchval(
            """
            SELECT count(*) FROM processed_events
            WHERE event_id = 'evt-partial-legacy-001'
            """,
        )
    finally:
        await connection.close()
    assert order is not None
    assert payment_event_id is not None
    return order["id"], order["status"], payment_event_id, partial_event_count


async def current_revision(database_url: str) -> str | None:
    connection = await asyncpg.connect(database_url)
    try:
        version_table_exists = await connection.fetchval(
            "SELECT to_regclass('public.alembic_version') IS NOT NULL",
        )
        if not version_table_exists:
            return None
        return await connection.fetchval("SELECT version_num FROM alembic_version")
    finally:
        await connection.close()


async def add_strong_stale_processed_events_schema(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            CREATE TABLE processed_events (
                event_id TEXT,
                event_type TEXT,
                aggregate_type TEXT,
                aggregate_id TEXT,
                processed_at TEXT
            );
            INSERT INTO processed_events (
                event_id, event_type, aggregate_type, aggregate_id, processed_at
            ) VALUES
                (NULL, NULL, NULL, NULL, 'invalid timestamp'),
                ('evt-stale-001', 'ORDER_STALE', 'order', 'order-stale-001', NULL)
            """,
        )
    finally:
        await connection.close()


async def strong_stale_source_state(
    database_url: str,
) -> tuple[str, str, str, tuple[str, ...]]:
    connection = await asyncpg.connect(database_url)
    try:
        order = await connection.fetchrow(
            "SELECT id, status FROM orders WHERE id = 'order-legacy-001'",
        )
        payment_event_id = await connection.fetchval(
            """
            SELECT event_id FROM processed_payment_events
            WHERE event_id = 'evt-payment-failed-legacy-001'
            """,
        )
        stale_rows = await connection.fetch(
            """
            SELECT concat_ws('|',
                COALESCE(event_id, '<NULL>'),
                COALESCE(event_type, '<NULL>'),
                COALESCE(aggregate_type, '<NULL>'),
                COALESCE(aggregate_id, '<NULL>'),
                COALESCE(processed_at, '<NULL>')
            ) AS value
            FROM processed_events
            ORDER BY event_id NULLS FIRST
            """,
        )
    finally:
        await connection.close()
    assert order is not None
    assert payment_event_id is not None
    return (
        order["id"],
        order["status"],
        payment_event_id,
        tuple(row["value"] for row in stale_rows),
    )


async def add_contract_shaped_outbox_with_invalid_row(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute(
            """
            CREATE TABLE outbox_events (
                event_id VARCHAR(128) PRIMARY KEY,
                event_type VARCHAR(128) NOT NULL,
                aggregate_type VARCHAR(64) NOT NULL,
                aggregate_id VARCHAR(64) NOT NULL,
                topic VARCHAR(128) NOT NULL,
                message_key VARCHAR(128) NOT NULL,
                payload JSONB NOT NULL,
                occurred_at TIMESTAMPTZ NOT NULL,
                attempts INTEGER NOT NULL DEFAULT 0,
                next_attempt_at TIMESTAMPTZ,
                last_error TEXT,
                published_at TIMESTAMPTZ,
                dead_lettered_at TIMESTAMPTZ
            );
            CREATE INDEX ix_outbox_events_pending ON outbox_events (
                published_at, dead_lettered_at, next_attempt_at
            );
            INSERT INTO outbox_events (
                event_id, event_type, aggregate_type, aggregate_id,
                topic, message_key, payload, occurred_at, attempts
            ) VALUES (
                'evt-outbox-invalid-001', 'ORDER_INVALID', 'order',
                'order-invalid-001', 'orders.invalid', 'order-invalid-001',
                '{}', '2026-07-01T00:00:00Z', -1
            );
            ALTER TABLE outbox_events ADD CONSTRAINT
                ck_outbox_events_attempts_nonnegative
                CHECK (attempts >= 0) NOT VALID
            """,
        )
    finally:
        await connection.close()


async def invalid_outbox_attempts(database_url: str) -> int:
    connection = await asyncpg.connect(database_url)
    try:
        return await connection.fetchval(
            """
            SELECT attempts FROM outbox_events
            WHERE event_id = 'evt-outbox-invalid-001'
            """,
        )
    finally:
        await connection.close()
