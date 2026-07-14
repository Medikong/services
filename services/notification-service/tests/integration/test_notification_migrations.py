import os
import subprocess
import sys
from collections.abc import Iterator
from datetime import UTC, datetime
from pathlib import Path

import anyio
import pytest
from app.postgres import PostgresNotificationRepository
from app.store import NotificationAlreadyRecorded, NotificationRecorded
from sqlalchemy import text
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from contracts import NotificationRequestedEvent
from tests.integration.migration_test_support import (
    create_strong_stale_schema,
    database_snapshot,
)

SERVICE_ROOT = Path(__file__).resolve().parents[2]
DATABASE_URL = os.getenv("NOTIFICATION_TEST_DATABASE_URL")

pytestmark = pytest.mark.skipif(
    DATABASE_URL is None,
    reason="NOTIFICATION_TEST_DATABASE_URL is required for PostgreSQL integration tests",
)


@pytest.fixture(autouse=True)
def clean_database() -> Iterator[None]:
    assert DATABASE_URL is not None
    anyio.run(_reset_database, DATABASE_URL)
    yield


def test_fresh_database_upgrades_to_notification_schema() -> None:
    # Given
    assert DATABASE_URL is not None

    # When
    result = _run_alembic("upgrade", "head")

    # Then
    assert result.returncode == 0, result.stderr
    tables = anyio.run(_table_names, DATABASE_URL)
    assert {"alembic_version", "notifications", "processed_events"} <= tables


def test_legacy_notification_defaults_to_order_confirmed_after_upgrade() -> None:
    # Given
    assert DATABASE_URL is not None
    anyio.run(_create_legacy_notifications, DATABASE_URL, False)

    # When
    result = _run_alembic("upgrade", "head")

    # Then
    assert result.returncode == 0, result.stderr
    notification_type = anyio.run(
        _scalar_string, DATABASE_URL, "SELECT type FROM notifications"
    )
    assert notification_type == "ORDER_CONFIRMED"


def test_upgrade_rejects_duplicate_event_ids_without_mutating_legacy_database() -> None:
    # Given
    assert DATABASE_URL is not None
    anyio.run(_create_legacy_notifications, DATABASE_URL, True)
    before = anyio.run(database_snapshot, DATABASE_URL)

    # When
    result = _run_alembic("upgrade", "head")

    # Then
    assert result.returncode != 0
    assert "duplicate event_id values: evt-legacy-001" in result.stderr
    assert "Traceback" not in result.stderr
    after = anyio.run(database_snapshot, DATABASE_URL)
    assert after == before
    assert "alembic_version" not in after.tables


def test_upgrade_rejects_strong_stale_conflict_without_mutation() -> None:
    # Given
    assert DATABASE_URL is not None
    anyio.run(create_strong_stale_schema, DATABASE_URL)
    before = anyio.run(database_snapshot, DATABASE_URL)

    # When
    result = _run_alembic("upgrade", "head")

    # Then
    assert result.returncode != 0
    assert "duplicate event_id values: evt-legacy-001" in result.stderr
    assert "Traceback" not in result.stderr
    after = anyio.run(database_snapshot, DATABASE_URL)
    assert after == before
    assert "alembic_version" not in after.tables


def test_duplicate_event_race_commits_notification_and_processed_event_once() -> None:
    # Given
    assert DATABASE_URL is not None
    assert _run_alembic("upgrade", "head").returncode == 0

    # When
    results = anyio.run(_record_concurrently, DATABASE_URL)

    # Then
    assert sum(isinstance(result, NotificationRecorded) for result in results) == 1
    assert (
        sum(isinstance(result, NotificationAlreadyRecorded) for result in results) == 1
    )
    assert (
        anyio.run(_scalar_int, DATABASE_URL, "SELECT count(*) FROM notifications") == 1
    )
    assert (
        anyio.run(_scalar_int, DATABASE_URL, "SELECT count(*) FROM processed_events")
        == 1
    )


def test_repeat_upgrade_is_idempotent() -> None:
    # Given
    first = _run_alembic("upgrade", "head")

    # When
    second = _run_alembic("upgrade", "head")

    # Then
    assert first.returncode == 0, first.stderr
    assert second.returncode == 0, second.stderr


def test_unmigrated_repository_is_not_ready() -> None:
    # Given
    assert DATABASE_URL is not None

    # When
    ready = anyio.run(_repository_is_ready, DATABASE_URL)

    # Then
    assert ready is False


def test_downgrade_is_explicitly_unsupported() -> None:
    # Given
    upgrade = _run_alembic("upgrade", "head")
    assert upgrade.returncode == 0, upgrade.stderr

    # When
    downgrade = _run_alembic("downgrade", "base")

    # Then
    assert downgrade.returncode != 0
    assert "downgrade is not supported" in downgrade.stderr.lower()
    assert "TypeError" not in downgrade.stderr


def _run_alembic(*arguments: str) -> subprocess.CompletedProcess[str]:
    assert DATABASE_URL is not None
    return subprocess.run(
        [sys.executable, "-m", "alembic", *arguments],
        cwd=SERVICE_ROOT,
        env={**os.environ, "DATABASE_URL": DATABASE_URL},
        capture_output=True,
        check=False,
        text=True,
        timeout=30,
    )


async def _reset_database(database_url: str) -> None:
    engine = create_async_engine(database_url)
    async with engine.begin() as connection:
        await connection.execute(text("DROP SCHEMA public CASCADE"))
        await connection.execute(text("CREATE SCHEMA public"))
    await engine.dispose()


async def _table_names(database_url: str) -> set[str]:
    engine = create_async_engine(database_url)
    async with engine.connect() as connection:
        rows = await connection.execute(
            text("SELECT tablename FROM pg_tables WHERE schemaname = 'public'"),
        )
        names = {str(row[0]) for row in rows}
    await engine.dispose()
    return names


async def _create_legacy_notifications(
    database_url: str, duplicate_event: bool
) -> None:
    engine = create_async_engine(database_url)
    async with engine.begin() as connection:
        await connection.execute(
            text(
                """
                CREATE TABLE notifications (
                    id VARCHAR(64) PRIMARY KEY,
                    event_id VARCHAR(128) NOT NULL,
                    user_id VARCHAR(64) NOT NULL,
                    order_id VARCHAR(64),
                    title VARCHAR(120) NOT NULL,
                    message VARCHAR(500) NOT NULL,
                    created_at TIMESTAMPTZ NOT NULL,
                    read BOOLEAN NOT NULL
                )
                """,
            ),
        )
        await connection.execute(
            text(
                """
                INSERT INTO notifications (
                    id, event_id, user_id, order_id, title, message, created_at, read
                ) VALUES (
                    'notification-legacy-001', 'evt-legacy-001', 'user-001', 'order-001',
                    'confirmed', 'legacy notification', '2026-07-14T00:00:00Z', false
                )
                """,
            ),
        )
        if duplicate_event:
            await connection.execute(
                text(
                    """
                    INSERT INTO notifications (
                        id, event_id, user_id, order_id, title, message, created_at, read
                    ) VALUES (
                        'notification-legacy-duplicate', 'evt-legacy-001', 'user-001',
                        'order-001', 'confirmed duplicate', 'legacy notification duplicate',
                        '2026-07-14T00:00:01Z', false
                    )
                    """,
                ),
            )
    await engine.dispose()


async def _record_concurrently(
    database_url: str,
) -> tuple[NotificationRecorded | NotificationAlreadyRecorded, ...]:
    engine = create_async_engine(database_url, pool_size=2, max_overflow=0)
    repository = PostgresNotificationRepository(
        async_sessionmaker(engine, expire_on_commit=False),
    )
    event = NotificationRequestedEvent(
        eventId="evt-race-001",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 14, tzinfo=UTC),
        producer="order-service",
        notificationId="notification-race-001",
        orderId="order-001",
        title="confirmed",
        message="race notification",
    )
    results: list[NotificationRecorded | NotificationAlreadyRecorded] = []

    async def record() -> None:
        results.append(await repository.record_notification_requested(event))

    async with anyio.create_task_group() as task_group:
        task_group.start_soon(record)
        task_group.start_soon(record)
    await engine.dispose()
    return tuple(results)


async def _repository_is_ready(database_url: str) -> bool:
    engine = create_async_engine(database_url)
    repository = PostgresNotificationRepository(async_sessionmaker(engine))
    ready = await repository.is_ready()
    await engine.dispose()
    return ready


async def _scalar_int(database_url: str, statement: str) -> int:
    engine = create_async_engine(database_url)
    async with engine.connect() as connection:
        value = int((await connection.execute(text(statement))).scalar_one())
    await engine.dispose()
    return value


async def _scalar_string(database_url: str, statement: str) -> str:
    engine = create_async_engine(database_url)
    async with engine.connect() as connection:
        value = str((await connection.execute(text(statement))).scalar_one())
    await engine.dispose()
    return value
