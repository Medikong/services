import json
import os
import sys
from pathlib import Path

import anyio
import asyncpg
import pytest

DATABASE_URL = os.getenv("TEST_DATABASE_URL")
SERVICE_ROOT = Path(__file__).resolve().parents[2]
pytestmark = pytest.mark.skipif(
    DATABASE_URL is None,
    reason="TEST_DATABASE_URL is required for inventory snapshot CLI tests",
)


async def _reset_and_upgrade() -> None:
    assert DATABASE_URL is not None
    connection = await asyncpg.connect(DATABASE_URL)
    try:
        await connection.execute("DROP SCHEMA public CASCADE")
        await connection.execute("CREATE SCHEMA public")
    finally:
        await connection.close()
    environment = os.environ.copy()
    environment["DATABASE_URL"] = DATABASE_URL
    process = await anyio.run_process(
        [sys.executable, "-m", "app.migrate", "upgrade", "head"],
        cwd=SERVICE_ROOT,
        env=environment,
        check=False,
    )
    assert process.returncode == 0, process.stderr.decode()


async def _run_snapshot_cli() -> tuple[int, str, str]:
    assert DATABASE_URL is not None
    environment = os.environ.copy()
    environment["DATABASE_URL"] = DATABASE_URL
    process = await anyio.run_process(
        [sys.executable, "-m", "app.inventory_snapshots"],
        cwd=SERVICE_ROOT,
        env=environment,
        check=False,
    )
    return process.returncode, process.stdout.decode(), process.stderr.decode()


async def _snapshot_rows() -> tuple[tuple[str, str], ...]:
    assert DATABASE_URL is not None
    connection = await asyncpg.connect(DATABASE_URL)
    try:
        rows = await connection.fetch(
            "SELECT event_id, payload::text FROM outbox_events "
            "WHERE event_type='inventory.changed' ORDER BY event_id",
        )
    finally:
        await connection.close()
    return tuple((row["event_id"], row["payload"]) for row in rows)


def test_cli_enqueues_fresh_snapshot_event_for_every_inventory_row() -> None:
    # Given
    anyio.run(_reset_and_upgrade)

    # When
    first_exit, first_stdout, first_stderr = anyio.run(_run_snapshot_cli)
    first_rows = anyio.run(_snapshot_rows)
    second_exit, second_stdout, second_stderr = anyio.run(_run_snapshot_cli)
    all_rows = anyio.run(_snapshot_rows)

    # Then
    assert first_exit == 0
    assert first_stdout.splitlines() == ["enqueued 2 inventory snapshot events"]
    assert first_stderr == ""
    assert second_exit == 0
    assert second_stdout.splitlines() == ["enqueued 2 inventory snapshot events"]
    assert second_stderr == ""
    assert len(first_rows) == 2
    assert len(all_rows) == 4
    first_ids = {event_id for event_id, _ in first_rows}
    second_ids = {event_id for event_id, _ in all_rows} - first_ids
    assert len(second_ids) == 2
    payloads = [json.loads(payload) for _, payload in all_rows]
    assert {
        (
            payload["dropId"],
            payload["productId"],
            payload["remainingQuantity"],
            payload["inventoryVersion"],
        )
        for payload in payloads
    } == {
        ("drop-001", "product-001", 42, 0),
        ("drop-sold-out-001", "product-sold-out-001", 42, 0),
    }
