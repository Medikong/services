from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import asyncpg

SERVICE_ROOT = Path(__file__).parents[1]


def run_migration(
    database_url: str, *arguments: str
) -> subprocess.CompletedProcess[str]:
    environment = os.environ.copy()
    environment["DATABASE_URL"] = database_url.replace(
        "postgresql://",
        "postgresql+asyncpg://",
        1,
    )
    return subprocess.run(
        [sys.executable, "-m", "app.migrate", *arguments],
        cwd=SERVICE_ROOT,
        env=environment,
        capture_output=True,
        text=True,
        check=False,
        timeout=60,
    )


async def reset_database(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute("DROP SCHEMA IF EXISTS public CASCADE")
        await connection.execute("CREATE SCHEMA public")
    finally:
        await connection.close()


async def remove_migration_stamp(database_url: str) -> None:
    connection = await asyncpg.connect(database_url)
    try:
        await connection.execute("DROP TABLE alembic_version")
    finally:
        await connection.close()
