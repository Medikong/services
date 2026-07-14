import os
import sys
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from uuid import uuid4

import anyio
from sqlalchemy import text
from sqlalchemy.engine import make_url
from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine

from app.records import Base, KnownOrderRecord, PaymentRecord

SERVICE_ROOT = Path(__file__).resolve().parents[2]


@dataclass(frozen=True, slots=True)
class TestDatabase:
    url: str


@dataclass(frozen=True, slots=True)
class MigrationResult:
    returncode: int
    stdout: str
    stderr: str


@asynccontextmanager
async def isolated_database(prefix: str) -> AsyncIterator[TestDatabase]:
    base_url = make_url(os.environ["PAYMENT_TEST_DATABASE_URL"])
    database_name = f"{prefix[:24]}_{uuid4().hex}"
    admin_engine = create_async_engine(
        base_url.set(database="postgres"),
        isolation_level="AUTOCOMMIT",
    )
    database_url = base_url.set(database=database_name).render_as_string(
        hide_password=False,
    )
    try:
        async with admin_engine.connect() as connection:
            await connection.execute(text(f'CREATE DATABASE "{database_name}"'))
        yield TestDatabase(url=database_url)
    finally:
        async with admin_engine.connect() as connection:
            await connection.execute(
                text(
                    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                    "WHERE datname = :database_name AND pid <> pg_backend_pid()",
                ),
                {"database_name": database_name},
            )
            await connection.execute(text(f'DROP DATABASE "{database_name}"'))
        await admin_engine.dispose()


async def create_legacy_schema(database_url: str) -> AsyncEngine:
    engine = create_async_engine(database_url)
    async with engine.begin() as connection:
        await connection.run_sync(
            Base.metadata.create_all,
            tables=[
                Base.metadata.tables[KnownOrderRecord.__tablename__],
                Base.metadata.tables[PaymentRecord.__tablename__],
            ],
        )
        await connection.execute(
            text(
                "ALTER TABLE payments "
                "DROP CONSTRAINT IF EXISTS uq_payments_order_id, "
                "DROP CONSTRAINT IF EXISTS fk_payments_order_id, "
                "DROP CONSTRAINT IF EXISTS ck_payments_amount_nonnegative, "
                "DROP CONSTRAINT IF EXISTS ck_payments_method, "
                "DROP CONSTRAINT IF EXISTS ck_payments_status, "
                "DROP CONSTRAINT IF EXISTS ck_payments_terminal_timestamps",
            ),
        )
        await connection.execute(
            text(
                "ALTER TABLE known_orders DROP CONSTRAINT IF EXISTS "
                "ck_known_orders_amount_nonnegative",
            ),
        )
    return engine


async def run_migration(database_url: str, *arguments: str) -> MigrationResult:
    process = await anyio.run_process(
        [
            sys.executable,
            "-m",
            "alembic",
            "-c",
            str(SERVICE_ROOT / "alembic.ini"),
            *arguments,
        ],
        cwd=SERVICE_ROOT,
        env={**os.environ, "DATABASE_URL": database_url},
        check=False,
    )
    return MigrationResult(
        returncode=process.returncode,
        stdout=process.stdout.decode(),
        stderr=process.stderr.decode(),
    )
