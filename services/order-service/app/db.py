import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass

import anyio
from fastapi import FastAPI
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker, create_async_engine

from app.messaging import (
    KafkaRuntime,
    NoopOrderEventPublisher,
    OrderEventPublisher,
    kafka_runtime_from_bootstrap,
)
from app.postgres import PostgresOrderRepository
from app.repository import OrderRepository
from app.store import OrderStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)  # noqa: MUTABLE_OK
class AppResources:
    """Resources mutated by lifespan after the async event loop starts."""

    repository: OrderRepository
    event_publisher: OrderEventPublisher
    engine: AsyncEngine | None = None
    kafka_bootstrap_servers: str = ""
    kafka_runtime: KafkaRuntime | None = None


def resources_from_env() -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    if database_url == "":
        repository = OrderStore()
        return AppResources(
            repository=repository,
            event_publisher=NoopOrderEventPublisher(),
            kafka_bootstrap_servers=kafka_bootstrap_servers,
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
    repository = PostgresOrderRepository(session_factory)
    return AppResources(
        repository=repository,
        event_publisher=NoopOrderEventPublisher(),
        engine=engine,
        kafka_bootstrap_servers=kafka_bootstrap_servers,
    )


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        runtime = resources.kafka_runtime
        try:
            if runtime is None and resources.kafka_bootstrap_servers != "":
                runtime = kafka_runtime_from_bootstrap(
                    resources.repository,
                    resources.kafka_bootstrap_servers,
                )
                resources.kafka_runtime = runtime
                if runtime.publisher is not None:
                    resources.event_publisher = runtime.publisher
            if runtime is not None and runtime.publisher is not None:
                await runtime.publisher.start()
            if runtime is not None and runtime.payment_consumer is not None:
                await runtime.payment_consumer.start()
            async with anyio.create_task_group() as task_group:
                if runtime is not None and runtime.payment_consumer is not None:
                    task_group.start_soon(runtime.payment_consumer.run)
                yield
                task_group.cancel_scope.cancel()
        finally:
            if runtime is not None and runtime.payment_consumer is not None:
                await runtime.payment_consumer.stop()
            if runtime is not None and runtime.publisher is not None:
                await runtime.publisher.stop()
            if resources.engine is not None:
                await resources.engine.dispose()

    return lifespan


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    if database_url.startswith("postgresql://"):
        return database_url.replace("postgresql://", "postgresql+asyncpg://", 1)
    return database_url
