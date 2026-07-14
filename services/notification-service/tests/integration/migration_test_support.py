from dataclasses import dataclass

from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncConnection, create_async_engine


@dataclass(frozen=True, slots=True)
class DatabaseSnapshot:
    tables: tuple[str, ...]
    columns: tuple[str, ...]
    constraints: tuple[str, ...]
    indexes: tuple[str, ...]
    notification_rows: tuple[str, ...]
    processed_event_rows: tuple[str, ...]


async def database_snapshot(database_url: str) -> DatabaseSnapshot:
    engine = create_async_engine(database_url)
    async with engine.connect() as connection:
        tables = await _query_lines(
            connection,
            "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1",
        )
        snapshot = DatabaseSnapshot(
            tables=tables,
            columns=await _query_lines(
                connection,
                "SELECT table_name,column_name,data_type,is_nullable,"
                "coalesce(column_default,'') FROM information_schema.columns "
                "WHERE table_schema='public' ORDER BY 1,ordinal_position",
            ),
            constraints=await _query_lines(
                connection,
                "SELECT conrelid::regclass::text,conname,contype,"
                "pg_get_constraintdef(oid) FROM pg_constraint "
                "WHERE connamespace='public'::regnamespace ORDER BY 1,2",
            ),
            indexes=await _query_lines(
                connection,
                "SELECT tablename,indexname,indexdef FROM pg_indexes "
                "WHERE schemaname='public' ORDER BY 1,2",
            ),
            notification_rows=await _query_lines(
                connection,
                "SELECT to_jsonb(n)::text FROM notifications n ORDER BY id",
            ),
            processed_event_rows=(
                await _query_lines(
                    connection,
                    "SELECT to_jsonb(p)::text FROM processed_events p "
                    "ORDER BY event_id",
                )
                if "processed_events" in tables
                else ()
            ),
        )
    await engine.dispose()
    return snapshot


async def create_strong_stale_schema(database_url: str) -> None:
    engine = create_async_engine(database_url)
    async with engine.begin() as connection:
        statements = (
            "CREATE TABLE notifications ("
            "id VARCHAR(64) PRIMARY KEY, event_id VARCHAR(128) NOT NULL, "
            "user_id VARCHAR(64) NOT NULL, order_id VARCHAR(64), "
            "type VARCHAR(32) NOT NULL DEFAULT 'ORDER_CONFIRMED', "
            "title VARCHAR(120) NOT NULL, message VARCHAR(500) NOT NULL, "
            "created_at TIMESTAMPTZ NOT NULL, read BOOLEAN NOT NULL, "
            "CONSTRAINT ck_notifications_type CHECK "
            "(type IN ('ORDER_CONFIRMED', 'PAYMENT_FAILED')))",
            "CREATE INDEX ix_notifications_user_created "
            "ON notifications (user_id, created_at)",
            "CREATE TABLE processed_events ("
            "event_id VARCHAR(128) PRIMARY KEY, event_type VARCHAR(128) NOT NULL, "
            "processed_at TIMESTAMPTZ NOT NULL)",
            "INSERT INTO processed_events VALUES "
            "('evt-unrelated', 'unrelated.event', '2026-07-13T00:00:00Z')",
            "INSERT INTO notifications VALUES "
            "('notification-legacy-001', 'evt-legacy-001', 'user-001', "
            "'order-001', 'ORDER_CONFIRMED', 'confirmed', 'first', "
            "'2026-07-14T00:00:00Z', false), "
            "('notification-legacy-duplicate', 'evt-legacy-001', 'user-001', "
            "'order-001', 'PAYMENT_FAILED', 'failed', 'second', "
            "'2026-07-14T00:00:01Z', false)",
        )
        for statement in statements:
            await connection.execute(text(statement))
    await engine.dispose()


async def _query_lines(
    connection: AsyncConnection,
    statement: str,
) -> tuple[str, ...]:
    rows = await connection.execute(text(statement))
    return tuple("|".join(str(value) for value in row) for row in rows)
