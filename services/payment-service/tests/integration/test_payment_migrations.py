from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine

from app.main import create_app
from tests.integration.migration_support import (
    create_legacy_schema,
    isolated_database,
    run_migration,
)

MIGRATION_HEAD = "20260714_02"


@pytest.mark.anyio
async def test_upgrade_creates_fresh_payment_schema() -> None:
    # Given
    async with isolated_database("payment_fresh") as database:
        # When
        result = await run_migration(database.url, "upgrade", "head")

        # Then
        assert result.returncode == 0, result.stderr
        engine = create_async_engine(database.url)
        async with engine.connect() as connection:
            tables = set(
                (
                    await connection.execute(
                        text(
                            "SELECT tablename FROM pg_tables "
                            "WHERE schemaname = 'public'",
                        ),
                    )
                ).scalars()
            )
            version = (
                await connection.execute(
                    text("SELECT version_num FROM alembic_version")
                )
            ).scalar_one()
            base_constraints = set(
                (
                    await connection.execute(
                        text(
                            "SELECT t.relname || ':' || c.conname FROM pg_constraint c "
                            "JOIN pg_class t ON t.oid = c.conrelid "
                            "WHERE t.relname IN ('known_orders', 'payments')",
                        ),
                    )
                ).scalars()
            )
        await engine.dispose()

        assert tables == {
            "alembic_version",
            "known_orders",
            "outbox_events",
            "payments",
            "processed_events",
            "refunds",
        }
        assert version == MIGRATION_HEAD
        assert base_constraints == {
            "known_orders:ck_known_orders_amount_nonnegative",
            "known_orders:known_orders_pkey",
            "payments:ck_payments_amount_nonnegative",
            "payments:ck_payments_method",
            "payments:ck_payments_status",
            "payments:ck_payments_terminal_timestamps",
            "payments:fk_payments_order_id",
            "payments:payments_pkey",
            "payments:uq_payments_order_id",
            "payments:uq_payments_user_idempotency_key",
        }


@pytest.mark.anyio
async def test_upgrade_preserves_current_payment_rows() -> None:
    # Given
    async with isolated_database("payment_legacy") as database:
        engine = await create_legacy_schema(database.url)
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO known_orders (order_id, user_id, amount, created_at) "
                    "VALUES ('order-approved', 'user-001', 50000, now()), "
                    "('order-failed', 'user-002', 30000, now())",
                ),
            )
            await connection.execute(
                text(
                    "INSERT INTO payments "
                    "(id, order_id, user_id, amount, method, status, idempotency_key, "
                    "created_at, approved_at, failed_at, failure_reason) VALUES "
                    "('payment-approved', 'order-approved', 'user-001', 50000, "
                    "'MOCK_CARD', 'APPROVED', 'approve-key', now(), now(), NULL, NULL), "
                    "('payment-failed', 'order-failed', 'user-002', 30000, "
                    "'MOCK_CARD', 'FAILED', 'fail-key', now(), NULL, now(), "
                    "'card_declined')",
                ),
            )

        # When
        result = await run_migration(database.url, "upgrade", "head")

        # Then
        assert result.returncode == 0, result.stderr
        async with engine.connect() as connection:
            rows = (
                (
                    await connection.execute(
                        text("SELECT id, order_id, status FROM payments ORDER BY id"),
                    )
                )
                .tuples()
                .all()
            )
        await engine.dispose()
        assert rows == [
            ("payment-approved", "order-approved", "APPROVED"),
            ("payment-failed", "order-failed", "FAILED"),
        ]


@pytest.mark.anyio
async def test_repeated_upgrade_is_a_no_op() -> None:
    # Given
    async with isolated_database("payment_repeat") as database:
        first = await run_migration(database.url, "upgrade", "head")

        # When
        second = await run_migration(database.url, "upgrade", "head")

        # Then
        assert first.returncode == 0, first.stderr
        assert second.returncode == 0, second.stderr
        engine = create_async_engine(database.url)
        async with engine.connect() as connection:
            versions = (
                (
                    await connection.execute(
                        text("SELECT version_num FROM alembic_version")
                    )
                )
                .scalars()
                .all()
            )
        await engine.dispose()
        assert versions == [MIGRATION_HEAD]


@pytest.mark.anyio
async def test_conflicting_legacy_payments_fail_without_changing_rows() -> None:
    # Given
    async with isolated_database("payment_conflict") as database:
        engine = await create_legacy_schema(database.url)
        async with engine.begin() as connection:
            await connection.execute(
                text(
                    "INSERT INTO payments "
                    "(id, order_id, user_id, amount, method, status, idempotency_key, "
                    "created_at, approved_at, failed_at, failure_reason) VALUES "
                    "('payment-approved', 'order-conflict', 'user-001', 50000, "
                    "'MOCK_CARD', 'APPROVED', 'approve-key', now(), now(), NULL, NULL), "
                    "('payment-failed', 'order-conflict', 'user-001', 50000, "
                    "'MOCK_CARD', 'FAILED', 'fail-key', now(), NULL, now(), "
                    "'card_declined')",
                ),
            )

        # When
        result = await run_migration(database.url, "upgrade", "head")

        # Then
        assert result.returncode != 0
        diagnostic = f"{result.stdout}\n{result.stderr}"
        assert "order-conflict" in diagnostic
        assert "APPROVED" in diagnostic
        assert "FAILED" in diagnostic
        assert "remove or reconcile the conflicting payment records" in diagnostic
        async with engine.connect() as connection:
            rows = (
                (
                    await connection.execute(
                        text("SELECT id, status FROM payments ORDER BY id"),
                    )
                )
                .tuples()
                .all()
            )
        await engine.dispose()
        assert rows == [
            ("payment-approved", "APPROVED"),
            ("payment-failed", "FAILED"),
        ]


@pytest.mark.anyio
async def test_unmigrated_database_fails_readiness(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    async with isolated_database("payment_unmigrated") as database:
        monkeypatch.setenv("DATABASE_URL", database.url)
        app = create_app()

        # When
        with TestClient(app) as client:
            response = client.get("/readyz")

        # Then
        assert response.status_code == 503
        assert response.json()["status"] == "not_ready"
        assert response.json()["checks"]["database_schema"] == "migration_required"


@pytest.mark.anyio
async def test_downgrade_is_explicitly_unsupported() -> None:
    # Given
    async with isolated_database("payment_downgrade") as database:
        upgrade = await run_migration(database.url, "upgrade", "head")
        assert upgrade.returncode == 0, upgrade.stderr

        # When
        downgrade = await run_migration(database.url, "downgrade", "-1")

        # Then
        assert downgrade.returncode != 0
        assert "payment-service migrations do not support downgrade" in (
            f"{downgrade.stdout}\n{downgrade.stderr}"
        )
        engine = create_async_engine(database.url)
        async with engine.connect() as connection:
            version = (
                await connection.execute(
                    text("SELECT version_num FROM alembic_version")
                )
            ).scalar_one()
        await engine.dispose()
        assert version == MIGRATION_HEAD


def test_payment_service_exposes_service_local_alembic_config() -> None:
    # Given
    service_root = Path(__file__).resolve().parents[2]

    # When
    config_exists = (service_root / "alembic.ini").is_file()

    # Then
    assert config_exists
