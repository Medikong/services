import asyncio
import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager, suppress
from dataclasses import dataclass, field

from fastapi import FastAPI
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker, create_async_engine

from app.counter_repository import DropInterestCounterRepository
from app.counter_store import DropInterestCounterStore
from app.messaging import (
    InterestEventPublisher,
    KafkaInterestEventPublisher,
    NoopInterestEventPublisher,
    kafka_runtime_from_bootstrap,
)
from app.postgres import (
    Base,
    PostgresDropInterestCounterRepository,
    PostgresDropViewCounterRepository,
    PostgresDropViewRankingRepository,
    PostgresDropViewRepository,
    PostgresInterestRepository,
)
from app.repository import InterestRepository
from app.store import InterestStore
from app.view_ranking_worker import run_view_ranking_worker_forever
from app.view_repository import DropViewCounterRepository, DropViewRankingRepository, DropViewRepository
from app.view_store import DropViewCounterStore, DropViewRankingStore, DropViewStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)
class AppResources:
    """Resources mutated by lifespan after the async event loop starts."""

    repository: InterestRepository
    counter_repository: DropInterestCounterRepository
    view_repository: DropViewRepository
    view_ranking_repository: DropViewRankingRepository
    view_counter_repository: DropViewCounterRepository
    event_publisher: InterestEventPublisher = field(default_factory=NoopInterestEventPublisher)
    engine: AsyncEngine | None = None
    kafka_publisher: KafkaInterestEventPublisher | None = None


def resources_from_env() -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_runtime = kafka_runtime_from_bootstrap(os.getenv("KAFKA_BOOTSTRAP_SERVERS", ""))
    event_publisher = kafka_runtime.publisher or NoopInterestEventPublisher()

    if database_url == "":
        view_store = DropViewStore()
        view_counter_store = DropViewCounterStore()
        interest_store = InterestStore()
        return AppResources(
            repository=interest_store,
            counter_repository=DropInterestCounterStore(view_counter_store),
            view_repository=view_store,
            view_ranking_repository=DropViewRankingStore(view_store, interest_store),
            view_counter_repository=view_counter_store,
            event_publisher=event_publisher,
            kafka_publisher=kafka_runtime.publisher,
        )

    engine = create_async_engine(
        _async_database_url(database_url),
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=0,
        pool_timeout=5.0,
        pool_recycle=1800,
    )
    session_factory = async_sessionmaker(engine, expire_on_commit=False)
    interest_repository = PostgresInterestRepository(session_factory)
    return AppResources(
        repository=interest_repository,
        counter_repository=PostgresDropInterestCounterRepository(session_factory),
        view_repository=PostgresDropViewRepository(session_factory),
        view_ranking_repository=PostgresDropViewRankingRepository(session_factory, interest_repository),
        view_counter_repository=PostgresDropViewCounterRepository(session_factory),
        event_publisher=event_publisher,
        engine=engine,
        kafka_publisher=kafka_runtime.publisher,
    )


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        worker_task: asyncio.Task[None] | None = None
        try:
            if resources.engine is not None:
                await create_schema(resources.engine)
            if resources.kafka_publisher is not None:
                await resources.kafka_publisher.start()
            worker_task = asyncio.create_task(
                run_view_ranking_worker_forever(resources.view_ranking_repository),
            )
            yield
        finally:
            if worker_task is not None:
                worker_task.cancel()
                with suppress(asyncio.CancelledError):
                    await worker_task
            if resources.kafka_publisher is not None:
                await resources.kafka_publisher.stop()
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
