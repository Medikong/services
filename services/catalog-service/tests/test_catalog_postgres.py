import os
import sys
from pathlib import Path

import anyio
import pytest
from httpx import ASGITransport, AsyncClient
from sqlalchemy import TextClause, text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import create_async_engine

from app.db import create_database
from app.main import create_app
from app.postgres import PostgresCatalogRepository

DATABASE_URL = os.getenv("TEST_DATABASE_URL")
pytestmark = [
    pytest.mark.anyio,
    pytest.mark.postgres,
    pytest.mark.skipif(
        DATABASE_URL is None,
        reason="TEST_DATABASE_URL is required for catalog PostgreSQL tests",
    ),
]
SERVICE_ROOT = Path(__file__).resolve().parents[1]


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


async def _reset_database() -> None:
    assert DATABASE_URL is not None
    engine = create_async_engine(DATABASE_URL)
    async with engine.begin() as connection:
        await connection.execute(text("DROP TABLE IF EXISTS alembic_version"))
        await connection.execute(text("DROP TABLE IF EXISTS processed_events"))
        await connection.execute(text("DROP TABLE IF EXISTS products"))
        await connection.execute(text("DROP TABLE IF EXISTS drops"))
        await connection.execute(text("DROP TABLE IF EXISTS legacy_catalog_marker"))
    await engine.dispose()


async def _upgrade() -> None:
    assert DATABASE_URL is not None
    environment = os.environ.copy()
    environment["DATABASE_URL"] = DATABASE_URL
    process = await anyio.run_process(
        [sys.executable, "-m", "app.migrations", "upgrade"],
        cwd=SERVICE_ROOT,
        env=environment,
        check=False,
    )
    assert process.returncode == 0, process.stderr.decode()


@pytest.mark.usefixtures("anyio_backend")
async def test_migration_seeds_catalog_and_preserves_existing_data() -> None:
    assert DATABASE_URL is not None
    await _reset_database()
    engine = create_async_engine(DATABASE_URL)
    async with engine.begin() as connection:
        await connection.execute(
            text("CREATE TABLE legacy_catalog_marker (id integer PRIMARY KEY)"),
        )
        await connection.execute(
            text("INSERT INTO legacy_catalog_marker (id) VALUES (7)"),
        )
    await _upgrade()

    database = create_database(DATABASE_URL)
    repository = PostgresCatalogRepository(database.sessions)
    transport = ASGITransport(app=create_app(repository=repository))
    async with AsyncClient(
        transport=transport,
        base_url="http://catalog.test",
    ) as client:
        list_response = await client.get("/drops")
        detail_response = await client.get("/drops/drop-001")
        readiness_response = await client.get("/readyz")

    assert list_response.status_code == 200
    drop_products = [
        (drop["id"], drop["products"][0]["id"]) for drop in list_response.json()["data"]
    ]
    assert drop_products == [
        ("drop-001", "product-001"),
        ("drop-sold-out-001", "product-sold-out-001"),
    ]
    product = detail_response.json()["data"]["products"][0]
    projection = (
        product["name"],
        product["price"],
        product["remainingQuantity"],
        product["inventoryVersion"],
    )
    assert projection == (
        "DropMong Starter Kit",
        50000,
        42,
        0,
    )
    assert readiness_response.status_code == 200
    async with engine.connect() as connection:
        marker = await connection.scalar(text("SELECT id FROM legacy_catalog_marker"))
    assert marker == 7
    await database.engine.dispose()
    await engine.dispose()


async def test_repeat_upgrade_is_noop_and_does_not_reseed_cleared_projection() -> None:
    assert DATABASE_URL is not None
    await _reset_database()
    await _upgrade()
    engine = create_async_engine(DATABASE_URL)
    async with engine.begin() as connection:
        await connection.execute(
            text(
                "UPDATE products SET remaining_quantity = 3, inventory_version = 9 "
                "WHERE id = 'product-001'"
            )
        )
        await connection.execute(
            text("DELETE FROM products WHERE id = 'product-sold-out-001'")
        )

    await _upgrade()

    async with engine.connect() as connection:
        projections = (
            await connection.execute(
                text(
                    "SELECT id, remaining_quantity, inventory_version "
                    "FROM products ORDER BY id"
                )
            )
        ).all()
    assert projections == [("product-001", 3, 9)]
    await engine.dispose()


@pytest.mark.parametrize(
    "statement",
    [
        text(
            "UPDATE products SET remaining_quantity = -1 WHERE id = 'product-001'",
        ),
        text(
            "UPDATE products SET inventory_version = -1 WHERE id = 'product-001'",
        ),
    ],
)
async def test_database_rejects_negative_inventory_projection(
    statement: TextClause,
) -> None:
    assert DATABASE_URL is not None
    await _reset_database()
    await _upgrade()
    engine = create_async_engine(DATABASE_URL)

    with pytest.raises(IntegrityError):
        async with engine.begin() as connection:
            await connection.execute(statement)
    await engine.dispose()


async def test_readiness_fails_against_unmigrated_database() -> None:
    assert DATABASE_URL is not None
    await _reset_database()
    database = create_database(DATABASE_URL)
    repository = PostgresCatalogRepository(database.sessions)
    transport = ASGITransport(app=create_app(repository=repository))

    async with AsyncClient(
        transport=transport,
        base_url="http://catalog.test",
    ) as client:
        response = await client.get("/readyz")

    assert response.status_code == 503
    assert response.json()["checks"] == {"catalog": "migration_required"}
    await database.engine.dispose()
