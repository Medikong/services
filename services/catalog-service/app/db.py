"""Catalog database resource construction."""

import os
from collections.abc import AsyncGenerator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass
from typing import Final

import anyio
import sqlalchemy.ext.asyncio as sqlalchemy_asyncio
from fastapi import FastAPI
from observability import (
    instrument_sqlalchemy,
    instrument_sqlalchemy_pool_events,
)
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
)

from app.messaging import InventoryConsumerFactory

DEFAULT_DATABASE_URL: Final = (
    "postgresql+asyncpg://app:app@postgres:5432/catalog_service"
)
type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(frozen=True, slots=True)
class Database:
    """Catalog database resources owned by the application lifespan."""

    engine: AsyncEngine
    sessions: async_sessionmaker[AsyncSession]


def create_database(database_url: str | None = None) -> Database:
    """Create the async engine and session factory without touching schema."""
    url = database_url or os.getenv("DATABASE_URL", DEFAULT_DATABASE_URL)
    instrument_sqlalchemy()
    engine = sqlalchemy_asyncio.create_async_engine(url, pool_pre_ping=True)
    instrument_sqlalchemy_pool_events(engine.sync_engine)
    return Database(
        engine=engine,
        sessions=async_sessionmaker(engine, expire_on_commit=False),
    )


def lifespan_for(
    database: Database | None,
    inventory_consumer_factory: InventoryConsumerFactory | None,
) -> FastAPILifespan:
    """Own the optional Kafka consumer and database resources."""

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncGenerator[None]:
        inventory_consumer = (
            inventory_consumer_factory()
            if inventory_consumer_factory is not None
            else None
        )
        consumer_started = False
        try:
            if inventory_consumer is not None:
                await inventory_consumer.start()
                consumer_started = True
            async with anyio.create_task_group() as task_group:
                if inventory_consumer is not None:
                    _task_handle = task_group.start_soon(inventory_consumer.run)
                yield
                task_group.cancel_scope.cancel()
        finally:
            if inventory_consumer is not None and consumer_started:
                await inventory_consumer.stop()
            if database is not None:
                await database.engine.dispose()

    return lifespan
