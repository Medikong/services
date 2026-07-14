import os
import sys
from datetime import UTC, datetime
from pathlib import Path

import anyio
import pytest
from contracts import InventoryChangedEvent
from httpx import ASGITransport, AsyncClient
from sqlalchemy import text
from sqlalchemy.ext.asyncio import create_async_engine

from app.db import create_database
from app.main import create_app
from app.postgres import PostgresCatalogRepository

DATABASE_URL = os.getenv("TEST_DATABASE_URL")
SERVICE_ROOT = Path(__file__).resolve().parents[1]
pytestmark = [
    pytest.mark.anyio,
    pytest.mark.postgres,
    pytest.mark.skipif(
        DATABASE_URL is None,
        reason="TEST_DATABASE_URL is required for catalog PostgreSQL tests",
    ),
]


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


async def _reset_and_upgrade() -> None:
    assert DATABASE_URL is not None
    engine = create_async_engine(DATABASE_URL)
    async with engine.begin() as connection:
        await connection.execute(text("DROP TABLE IF EXISTS alembic_version"))
        await connection.execute(
            text("DROP TABLE IF EXISTS inventory_projections"),
        )
        await connection.execute(text("DROP TABLE IF EXISTS processed_events"))
        await connection.execute(text("DROP TABLE IF EXISTS products"))
        await connection.execute(text("DROP TABLE IF EXISTS drops"))
    await engine.dispose()
    environment = os.environ.copy()
    environment["DATABASE_URL"] = DATABASE_URL
    process = await anyio.run_process(
        [sys.executable, "-m", "app.migrations", "upgrade"],
        cwd=SERVICE_ROOT,
        env=environment,
        check=False,
    )
    assert process.returncode == 0, process.stderr.decode()


def _inventory_event(
    *,
    event_id: str,
    product_id: str,
    remaining_quantity: int,
    inventory_version: int,
) -> InventoryChangedEvent:
    return InventoryChangedEvent(
        eventId=event_id,
        userId="system",
        sourceId=f"inventory:{product_id}",
        occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
        producer="order-service",
        correlationId=event_id,
        dropId=(
            "drop-sold-out-001" if product_id == "product-sold-out-001" else "drop-001"
        ),
        productId=product_id,
        totalQuantity=42,
        reservedQuantity=0,
        soldQuantity=42 - remaining_quantity,
        remainingQuantity=remaining_quantity,
        inventoryVersion=inventory_version,
    )


async def test_projection_keeps_highest_version_and_derives_drop_status() -> None:
    # Given
    assert DATABASE_URL is not None
    await _reset_and_upgrade()
    database = create_database(DATABASE_URL)
    repository = PostgresCatalogRepository(database.sessions)
    deliveries = (
        _inventory_event(
            event_id="evt-v3",
            product_id="product-001",
            remaining_quantity=7,
            inventory_version=3,
        ),
        _inventory_event(
            event_id="evt-v1",
            product_id="product-001",
            remaining_quantity=31,
            inventory_version=1,
        ),
        _inventory_event(
            event_id="evt-v2",
            product_id="product-001",
            remaining_quantity=19,
            inventory_version=2,
        ),
        _inventory_event(
            event_id="evt-v3-replayed",
            product_id="product-001",
            remaining_quantity=1,
            inventory_version=3,
        ),
    )

    # When
    for event in deliveries:
        await repository.apply_inventory_changed(event)

    # Then
    drop = await repository.get_drop("drop-001")
    assert drop is not None
    assert drop.status.value == "OPEN"
    assert drop.description == (
        "한정 수량으로 판매되는 DropMong 첫 번째 공개 드롭입니다."
    )
    assert (
        drop.products[0].remaining_quantity,
        drop.products[0].inventory_version,
    ) == (7, 3)
    engine = create_async_engine(DATABASE_URL)
    async with engine.connect() as connection:
        inbox_count = await connection.scalar(
            text("SELECT count(*) FROM processed_events"),
        )
    assert inbox_count == 4

    await repository.apply_inventory_changed(
        _inventory_event(
            event_id="evt-product-001-zero",
            product_id="product-001",
            remaining_quantity=0,
            inventory_version=4,
        ),
    )
    await repository.apply_inventory_changed(
        _inventory_event(
            event_id="evt-product-sold-out-zero",
            product_id="product-sold-out-001",
            remaining_quantity=0,
            inventory_version=1,
        ),
    )
    sold_out_drop = await repository.get_drop("drop-001")
    assert sold_out_drop is not None
    assert sold_out_drop.status.value == "SOLD_OUT"
    await engine.dispose()
    await database.engine.dispose()


async def test_snapshot_rebuild_preserves_catalog_owned_metadata() -> None:
    # Given
    assert DATABASE_URL is not None
    await _reset_and_upgrade()
    engine = create_async_engine(DATABASE_URL)
    async with engine.begin() as connection:
        await connection.execute(text("DELETE FROM inventory_projections"))
    database = create_database(DATABASE_URL)
    repository = PostgresCatalogRepository(database.sessions)

    # When
    await repository.apply_inventory_changed(
        _inventory_event(
            event_id="evt-snapshot-product-001",
            product_id="product-001",
            remaining_quantity=23,
            inventory_version=8,
        ),
    )

    # Then
    transport = ASGITransport(app=create_app(repository=repository))
    async with AsyncClient(
        transport=transport,
        base_url="http://catalog.test",
    ) as client:
        response = await client.get("/drops/drop-001")
    assert response.status_code == 200
    body = response.json()["data"]
    assert body["description"] == (
        "한정 수량으로 판매되는 DropMong 첫 번째 공개 드롭입니다."
    )
    assert body["products"][0] == {
        "id": "product-001",
        "name": "DropMong Starter Kit",
        "price": 50000,
        "remainingQuantity": 23,
        "inventoryVersion": 8,
    }
    await database.engine.dispose()
    await engine.dispose()


async def test_projection_follows_order_inventory_lifecycle_quantities() -> None:
    # Given
    assert DATABASE_URL is not None
    await _reset_and_upgrade()
    database = create_database(DATABASE_URL)
    repository = PostgresCatalogRepository(database.sessions)
    lifecycle = (
        ("reserve", 32, 1),
        ("release", 42, 2),
        ("reserve-again", 32, 3),
        ("sale", 32, 4),
        ("refund", 42, 5),
    )

    # When
    observed: list[tuple[int, int]] = []
    for transition, remaining_quantity, inventory_version in lifecycle:
        await repository.apply_inventory_changed(
            _inventory_event(
                event_id=f"evt-{transition}",
                product_id="product-001",
                remaining_quantity=remaining_quantity,
                inventory_version=inventory_version,
            ),
        )
        drop = await repository.get_drop("drop-001")
        assert drop is not None
        observed.append(
            (
                drop.products[0].remaining_quantity,
                drop.products[0].inventory_version,
            ),
        )

    # Then
    assert observed == [(32, 1), (42, 2), (32, 3), (32, 4), (42, 5)]
    await database.engine.dispose()
