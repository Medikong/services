from dataclasses import dataclass

import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine

from tests.integration.migration_support import (
    create_legacy_schema,
    isolated_database,
    run_migration,
)


@dataclass(frozen=True, slots=True)
class SchemaMutation:
    name: str
    statement: str


MALFORMED_SCHEMAS = (
    SchemaMutation("missing_column", "ALTER TABLE payments DROP COLUMN failure_reason"),
    SchemaMutation(
        "missing_known_order_column",
        "ALTER TABLE known_orders DROP COLUMN created_at",
    ),
    SchemaMutation(
        "wrong_type",
        "ALTER TABLE payments ALTER COLUMN amount TYPE bigint",
    ),
    SchemaMutation(
        "wrong_nullability",
        "ALTER TABLE payments ALTER COLUMN user_id DROP NOT NULL",
    ),
    SchemaMutation(
        "wrong_primary_key",
        "ALTER TABLE payments DROP CONSTRAINT payments_pkey, "
        "ADD CONSTRAINT payments_pkey PRIMARY KEY (order_id)",
    ),
    SchemaMutation(
        "wrong_unique",
        "ALTER TABLE payments DROP CONSTRAINT uq_payments_user_idempotency_key",
    ),
    SchemaMutation(
        "wrong_foreign_key",
        "ALTER TABLE payments ADD CONSTRAINT fk_payments_order_id "
        "FOREIGN KEY (order_id) REFERENCES known_orders(order_id) ON DELETE CASCADE",
    ),
    SchemaMutation(
        "wrong_check",
        "ALTER TABLE payments ADD CONSTRAINT ck_payments_amount_nonnegative "
        "CHECK (amount >= -1)",
    ),
    SchemaMutation(
        "wrong_index",
        "DROP INDEX ix_payments_order_id; "
        "CREATE INDEX ix_payments_order_id ON payments (user_id)",
    ),
)


@pytest.mark.anyio
@pytest.mark.parametrize("mutation", MALFORMED_SCHEMAS, ids=lambda item: item.name)
async def test_malformed_legacy_schema_fails_without_mutation(
    mutation: SchemaMutation,
) -> None:
    # Given
    async with isolated_database(f"malformed_{mutation.name}") as database:
        engine = await create_legacy_schema(database.url)
        await _seed_valid_legacy_row(engine)
        async with engine.begin() as connection:
            for statement in mutation.statement.split("; "):
                await connection.execute(text(statement))
        before_schema = await _schema_fingerprint(engine)
        before_rows = await _row_fingerprint(engine)

        # When
        result = await run_migration(database.url, "upgrade", "head")

        # Then
        assert result.returncode != 0
        assert "legacy payment schema is incompatible" in result.stderr
        assert "TypeError: super(type, obj)" not in result.stderr
        assert await _migration_version(engine) is None
        assert await _schema_fingerprint(engine) == before_schema
        assert await _row_fingerprint(engine) == before_rows
        assert await _target_tables(engine) == ()
        await engine.dispose()


@pytest.mark.anyio
async def test_invalid_legacy_rows_fail_preflight_without_mutation() -> None:
    # Given
    async with isolated_database("invalid_legacy_rows") as database:
        engine = await create_legacy_schema(database.url)
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO known_orders (order_id, user_id, amount, created_at) "
                    "VALUES ('order-invalid', 'user-001', -1, now())",
                ),
            )
            await connection.execute(
                text(
                    "INSERT INTO payments "
                    "(id, order_id, user_id, amount, method, status, idempotency_key, "
                    "created_at, approved_at, failed_at, failure_reason) VALUES "
                    "('payment-invalid', 'order-missing', 'user-001', -1, "
                    "'WIRE', 'PENDING', 'invalid-key', now(), NULL, NULL, NULL)",
                ),
            )
        before_schema = await _schema_fingerprint(engine)
        before_rows = await _row_fingerprint(engine)

        # When
        result = await run_migration(database.url, "upgrade", "head")

        # Then
        assert result.returncode != 0
        assert "legacy payment rows violate the target schema" in result.stderr
        assert await _migration_version(engine) is None
        assert await _schema_fingerprint(engine) == before_schema
        assert await _row_fingerprint(engine) == before_rows
        assert await _target_tables(engine) == ()
        await engine.dispose()


async def _seed_valid_legacy_row(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.execute(
            text(
                "INSERT INTO known_orders (order_id, user_id, amount, created_at) "
                "VALUES ('order-001', 'user-001', 50000, now())",
            ),
        )
        await connection.execute(
            text(
                "INSERT INTO payments "
                "(id, order_id, user_id, amount, method, status, idempotency_key, "
                "created_at, approved_at, failed_at, failure_reason) VALUES "
                "('payment-001', 'order-001', 'user-001', 50000, 'MOCK_CARD', "
                "'APPROVED', 'key-001', now(), now(), NULL, NULL)",
            ),
        )


async def _schema_fingerprint(engine: AsyncEngine) -> str:
    async with engine.connect() as connection:
        return (
            await connection.execute(
                text(
                    "SELECT jsonb_build_object("
                    "'columns', (SELECT jsonb_agg(row_to_json(c)::jsonb ORDER BY "
                    "c.table_name, c.ordinal_position) FROM information_schema.columns c "
                    "WHERE c.table_schema = 'public' AND c.table_name IN "
                    "('known_orders', 'payments')), "
                    "'constraints', (SELECT jsonb_agg(pg_get_constraintdef(k.oid) "
                    "ORDER BY t.relname, k.conname) FROM pg_constraint k "
                    "JOIN pg_class t ON t.oid = k.conrelid "
                    "JOIN pg_namespace n ON n.oid = t.relnamespace "
                    "WHERE n.nspname = 'public' AND t.relname IN "
                    "('known_orders', 'payments')), "
                    "'indexes', (SELECT jsonb_agg(indexdef ORDER BY tablename, indexname) "
                    "FROM pg_indexes WHERE schemaname = 'public' AND tablename IN "
                    "('known_orders', 'payments')))::text",
                ),
            )
        ).scalar_one()


async def _row_fingerprint(engine: AsyncEngine) -> tuple[int, int]:
    async with engine.connect() as connection:
        known_orders = (
            await connection.execute(text("SELECT count(*) FROM known_orders"))
        ).scalar_one()
        payments = (
            await connection.execute(text("SELECT count(*) FROM payments"))
        ).scalar_one()
    return known_orders, payments


async def _migration_version(engine: AsyncEngine) -> str | None:
    async with engine.connect() as connection:
        version_table = (
            await connection.execute(text("SELECT to_regclass('alembic_version')"))
        ).scalar_one()
        if version_table is None:
            return None
        return (
            await connection.execute(text("SELECT version_num FROM alembic_version"))
        ).scalar_one_or_none()


async def _target_tables(engine: AsyncEngine) -> tuple[str, ...]:
    async with engine.connect() as connection:
        rows = (
            await connection.execute(
                text(
                    "SELECT tablename FROM pg_tables WHERE schemaname = 'public' "
                    "AND tablename IN ('refunds', 'processed_events', 'outbox_events') "
                    "ORDER BY tablename",
                ),
            )
        ).scalars()
        return tuple(rows)
