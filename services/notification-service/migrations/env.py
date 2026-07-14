import os
from logging.config import fileConfig
from typing import override

import anyio
from alembic import context
from app.postgres import Base
from sqlalchemy import Connection, pool
from sqlalchemy.ext.asyncio import AsyncEngine, create_async_engine


class MissingDatabaseUrlError(RuntimeError):
    def __init__(self, variable: str) -> None:
        self.variable = variable

    @override
    def __str__(self) -> str:
        return f"{self.variable} is required to run notification migrations"


config = context.config
if config.config_file_name is not None:
    fileConfig(config.config_file_name)
target_metadata = Base.metadata


def database_url_from_env() -> str:
    database_url = os.getenv("DATABASE_URL")
    if database_url is None or database_url == "":
        raise MissingDatabaseUrlError(variable="DATABASE_URL")
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    return database_url


def run_migrations_offline() -> None:
    context.configure(
        url=database_url_from_env(),
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
    )
    with context.begin_transaction():
        context.run_migrations()


def run_sync_migrations(connection: Connection) -> None:
    context.configure(connection=connection, target_metadata=target_metadata)
    with context.begin_transaction():
        context.run_migrations()


async def run_async_migrations() -> None:
    engine: AsyncEngine = create_async_engine(
        database_url_from_env(),
        poolclass=pool.NullPool,
    )
    async with engine.connect() as connection:
        await connection.run_sync(run_sync_migrations)
    await engine.dispose()


def run_migrations_online() -> None:
    anyio.run(run_async_migrations)


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
