import os
from dataclasses import dataclass
from logging.config import fileConfig
from typing import override

import anyio
from alembic import context
from sqlalchemy import Connection, pool
from sqlalchemy.ext.asyncio import async_engine_from_config

from app.ledger import RefundRecord
from app.records import Base, OutboxEventRecord, ProcessedEventRecord

config = context.config
if config.config_file_name is not None:
    fileConfig(config.config_file_name)

target_metadata = Base.metadata
_MODELS = (OutboxEventRecord, ProcessedEventRecord, RefundRecord)


@dataclass(frozen=True, slots=True)
class MissingDatabaseUrlError(Exception):
    variable_name: str = "DATABASE_URL"

    @override
    def __str__(self) -> str:
        return f"{self.variable_name} must be set for payment-service migrations"


def _database_url() -> str:
    database_url = os.getenv("DATABASE_URL", "")
    if database_url == "":
        raise MissingDatabaseUrlError
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace(
            "postgresql+psycopg://",
            "postgresql+asyncpg://",
            1,
        )
    return database_url


def _run_migrations(connection: Connection) -> None:
    context.configure(
        connection=connection,
        target_metadata=target_metadata,
        compare_type=True,
    )
    with context.begin_transaction():
        context.run_migrations()


async def _run_online_migrations() -> None:
    section = config.get_section(config.config_ini_section) or {}
    section["sqlalchemy.url"] = _database_url()
    connectable = async_engine_from_config(
        section,
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )
    async with connectable.connect() as connection:
        await connection.run_sync(_run_migrations)
    await connectable.dispose()


if context.is_offline_mode():
    context.configure(
        url=_database_url(),
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
    )
    with context.begin_transaction():
        context.run_migrations()
else:
    anyio.run(_run_online_migrations)
