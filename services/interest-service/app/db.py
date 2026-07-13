import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass

from fastapi import FastAPI
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker, create_async_engine

from app.postgres import Base, PostgresInterestRepository
from app.repository import InterestRepository
from app.store import InterestStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)
class AppResources:
    """Resources mutated by lifespan after the async event loop starts."""

    repository: InterestRepository
    engine: AsyncEngine | None = None


def resources_from_env() -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    if database_url == "":
        return AppResources(repository=InterestStore())

    engine = create_async_engine(
        _async_database_url(database_url),
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=0,
        pool_timeout=5.0,
        pool_recycle=1800,
    )
    session_factory = async_sessionmaker(engine, expire_on_commit=False)
    return AppResources(repository=PostgresInterestRepository(session_factory), engine=engine)


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        try:
            if resources.engine is not None:
                await create_schema(resources.engine)
            yield
        finally:
            if resources.engine is not None:
                await resources.engine.dispose()

    return lifespan


async def create_schema(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    return database_url
