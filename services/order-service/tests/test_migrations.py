from __future__ import annotations

import os
from collections.abc import Iterator

import anyio
import asyncpg
import pytest
from fastapi.testclient import TestClient

from tests.migration_cli import remove_migration_stamp, reset_database, run_migration
from tests.migration_support import (
    add_contradictory_order,
    add_fresh_order,
    create_current_schema_with_rows,
    insert_invalid_cancellation_request,
    order_columns,
    read_preserved_rows,
    repair_contradictory_order,
    schema_state,
)
from tests.stale_schema_support import (
    add_contract_shaped_outbox_with_invalid_row,
    add_partial_processed_events_schema,
    add_strong_stale_processed_events_schema,
    current_revision,
    legacy_source_state,
    invalid_outbox_attempts,
    strong_stale_source_state,
)

TEST_DATABASE_ENV = "TEST_DATABASE_URL"
EXPECTED_TABLES = (
    "alembic_version",
    "cancellation_requests",
    "inventory_items",
    "orders",
    "outbox_events",
    "processed_events",
    "processed_payment_events",
)


@pytest.fixture(autouse=True)
def clean_database() -> Iterator[str]:
    database_url = os.getenv(TEST_DATABASE_ENV)
    if database_url is None:
        pytest.fail(f"{TEST_DATABASE_ENV} is required for migration tests")
    anyio.run(reset_database, database_url)
    yield database_url
    anyio.run(reset_database, database_url)


def test_upgrade_preserves_current_unversioned_order_rows(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)

    # When
    result = run_migration(clean_database, "upgrade")

    # Then
    assert result.returncode == 0, result.stderr
    preserved = anyio.run(read_preserved_rows, clean_database)
    assert preserved == (
        "order-legacy-001",
        "CONFIRMED",
        "NOT_STARTED",
        "evt-payment-failed-legacy-001",
    )


def test_upgrade_creates_versioned_schema_on_fresh_database(
    clean_database: str,
) -> None:
    # Given
    expected_lifecycle_columns = {
        "fulfillment_status",
        "expires_at",
        "cancel_pending_at",
        "canceled_at",
    }

    # When
    result = run_migration(clean_database, "upgrade", "head")

    # Then
    assert result.returncode == 0, result.stderr
    tables, revision, inventory_count = anyio.run(schema_state, clean_database)
    assert tables == EXPECTED_TABLES
    assert revision == "20260714_0001"
    assert inventory_count == 2
    assert expected_lifecycle_columns <= anyio.run(order_columns, clean_database)


def test_repeated_upgrade_is_a_no_op(clean_database: str) -> None:
    # Given
    first_result = run_migration(clean_database, "upgrade")
    first_state = anyio.run(schema_state, clean_database)

    # When
    second_result = run_migration(clean_database, "upgrade")

    # Then
    assert first_result.returncode == 0, first_result.stderr
    assert second_result.returncode == 0, second_result.stderr
    assert anyio.run(schema_state, clean_database) == first_state


def test_readiness_fails_without_migration(
    clean_database: str,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    from app.main import create_app

    monkeypatch.setenv(
        "DATABASE_URL",
        clean_database.replace("postgresql://", "postgresql+asyncpg://", 1),
    )

    # When
    with TestClient(create_app()) as client:
        response = client.get("/readyz")

    # Then
    assert response.status_code == 503
    assert response.json()["checks"]["database_migration"] == "failed"


def test_contradictory_legacy_inventory_stops_without_changing_rows(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_contradictory_order, clean_database)

    # When
    failed_result = run_migration(clean_database, "upgrade")

    # Then
    assert failed_result.returncode != 0
    assert "legacy inventory contradiction" in failed_result.stderr
    assert anyio.run(repair_contradictory_order, clean_database) == 2
    resumed_result = run_migration(clean_database, "upgrade")
    assert resumed_result.returncode == 0, resumed_result.stderr


def test_downgrade_fails_with_unsupported_diagnostic(clean_database: str) -> None:
    # Given
    upgrade_result = run_migration(clean_database, "upgrade")

    # When
    downgrade_result = run_migration(clean_database, "downgrade", "base")

    # Then
    assert upgrade_result.returncode == 0, upgrade_result.stderr
    assert downgrade_result.returncode == 2
    assert "Downgrade is unsupported" in downgrade_result.stderr
    assert "Traceback" not in downgrade_result.stderr


def test_partial_processed_events_schema_is_rejected_without_stamping_head(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_partial_processed_events_schema, clean_database)

    # When
    result = run_migration(clean_database, "upgrade")

    # Then
    assert result.returncode != 0
    assert "processed_events is missing required columns" in result.stderr
    assert anyio.run(current_revision, clean_database) is None
    assert anyio.run(legacy_source_state, clean_database) == (
        "order-legacy-001",
        "CONFIRMED",
        "evt-payment-failed-legacy-001",
        1,
    )


def test_readiness_fails_after_partial_schema_migration_is_rejected(
    clean_database: str,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    from app.main import create_app

    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_partial_processed_events_schema, clean_database)
    failed_result = run_migration(clean_database, "upgrade")
    assert failed_result.returncode != 0
    monkeypatch.setenv(
        "DATABASE_URL",
        clean_database.replace("postgresql://", "postgresql+asyncpg://", 1),
    )

    # When
    with TestClient(create_app()) as client:
        response = client.get("/readyz")

    # Then
    assert response.status_code == 503
    assert response.json()["checks"]["database_migration"] == "failed"


def test_fresh_schema_rejects_unknown_cancellation_refund_status(
    clean_database: str,
) -> None:
    # Given
    migration_result = run_migration(clean_database, "upgrade")
    assert migration_result.returncode == 0, migration_result.stderr
    anyio.run(add_fresh_order, clean_database)

    # When / Then
    with pytest.raises(asyncpg.CheckViolationError):
        anyio.run(
            insert_invalid_cancellation_request,
            clean_database,
            "order-fresh-001",
        )


def test_legacy_schema_rejects_unknown_cancellation_refund_status(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)
    migration_result = run_migration(clean_database, "upgrade")
    assert migration_result.returncode == 0, migration_result.stderr

    # When / Then
    with pytest.raises(asyncpg.CheckViolationError):
        anyio.run(
            insert_invalid_cancellation_request,
            clean_database,
            "order-legacy-001",
        )


def test_invalid_cli_command_returns_usage_without_traceback(
    clean_database: str,
) -> None:
    # Given / When
    result = run_migration(clean_database, "nonsense")

    # Then
    assert result.returncode == 2
    assert "usage: python -m app.migrate" in result.stderr
    assert "Traceback" not in result.stderr


def test_strong_stale_processed_events_contract_is_rejected_without_data_loss(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_strong_stale_processed_events_schema, clean_database)
    expected_source_state = (
        "order-legacy-001",
        "CONFIRMED",
        "evt-payment-failed-legacy-001",
        (
            "<NULL>|<NULL>|<NULL>|<NULL>|invalid timestamp",
            "evt-stale-001|ORDER_STALE|order|order-stale-001|<NULL>",
        ),
    )

    # When
    result = run_migration(clean_database, "upgrade", "head")

    # Then
    assert result.returncode != 0
    assert "processed_events contract mismatch" in result.stderr
    assert anyio.run(current_revision, clean_database) is None
    assert anyio.run(strong_stale_source_state, clean_database) == expected_source_state


def test_readiness_fails_after_strong_stale_schema_is_rejected(
    clean_database: str,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    from app.main import create_app

    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_strong_stale_processed_events_schema, clean_database)
    failed_result = run_migration(clean_database, "upgrade", "head")
    assert failed_result.returncode != 0
    monkeypatch.setenv(
        "DATABASE_URL",
        clean_database.replace("postgresql://", "postgresql+asyncpg://", 1),
    )

    # When
    with TestClient(create_app()) as client:
        response = client.get("/readyz")

    # Then
    assert response.status_code == 503
    assert response.json()["checks"]["database_migration"] == "failed"


def test_exact_unversioned_target_table_contract_is_accepted(
    clean_database: str,
) -> None:
    # Given
    first_result = run_migration(clean_database, "upgrade", "head")
    assert first_result.returncode == 0, first_result.stderr
    anyio.run(remove_migration_stamp, clean_database)

    # When
    second_result = run_migration(clean_database, "upgrade", "head")

    # Then
    assert second_result.returncode == 0, second_result.stderr
    assert anyio.run(current_revision, clean_database) == "20260714_0001"


def test_contract_shaped_target_with_incompatible_rows_is_rejected(
    clean_database: str,
) -> None:
    # Given
    anyio.run(create_current_schema_with_rows, clean_database)
    anyio.run(add_contract_shaped_outbox_with_invalid_row, clean_database)

    # When
    result = run_migration(clean_database, "upgrade", "head")

    # Then
    assert result.returncode != 0
    assert (
        "outbox_events contract mismatch: contains 1 incompatible rows" in result.stderr
    )
    assert anyio.run(current_revision, clean_database) is None
    assert anyio.run(invalid_outbox_attempts, clean_database) == -1
