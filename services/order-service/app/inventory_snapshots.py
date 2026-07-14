"""Enqueue authoritative inventory snapshots through the order Outbox."""

import sys
from datetime import UTC, datetime
from uuid import uuid4

import anyio
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.db import resources_from_env
from app.events import inventory_snapshot_event
from app.outbox import add_outbox_event
from app.records import InventoryItemRecord


async def enqueue_inventory_snapshots(
    sessions: async_sessionmaker[AsyncSession],
) -> int:
    """Stage one fresh snapshot event for every authoritative inventory row."""
    async with sessions.begin() as session:
        inventory_rows = (
            await session.scalars(
                select(InventoryItemRecord).order_by(
                    InventoryItemRecord.drop_id,
                    InventoryItemRecord.product_id,
                ),
            )
        ).all()
        occurred_at = datetime.now(UTC)
        for inventory in inventory_rows:
            event_id = f"evt-inventory-snapshot-{uuid4().hex}"
            add_outbox_event(
                session,
                inventory_snapshot_event(inventory, event_id, occurred_at),
            )
    return len(inventory_rows)


async def _run() -> int:
    resources = resources_from_env()
    if resources.session_factory is None or resources.engine is None:
        _ = sys.stderr.write("DATABASE_URL is required\n")
        return 2
    try:
        count = await enqueue_inventory_snapshots(resources.session_factory)
    finally:
        await resources.engine.dispose()
    _ = sys.stdout.write(f"enqueued {count} inventory snapshot events\n")
    return 0


def main() -> int:
    """Run the inventory snapshot enqueue command."""
    return anyio.run(_run)


if __name__ == "__main__":
    raise SystemExit(main())
