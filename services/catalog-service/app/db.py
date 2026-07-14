"""Catalog database resource construction."""

import os
from dataclasses import dataclass
from typing import Final

from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

DEFAULT_DATABASE_URL: Final = (
    "postgresql+asyncpg://app:app@postgres:5432/catalog_service"
)


@dataclass(frozen=True, slots=True)
class Database:
    """Catalog database resources owned by the application lifespan."""

    engine: AsyncEngine
    sessions: async_sessionmaker[AsyncSession]


def create_database(database_url: str | None = None) -> Database:
    """Create the async engine and session factory without touching schema."""
    url = database_url or os.getenv("DATABASE_URL", DEFAULT_DATABASE_URL)
    engine = create_async_engine(url, pool_pre_ping=True)
    return Database(
        engine=engine,
        sessions=async_sessionmaker(engine, expire_on_commit=False),
    )
