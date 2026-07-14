import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass

import anyio
from fastapi import FastAPI
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker, create_async_engine

from app.messaging import (
    KafkaRuntime,
    NoopPaymentEventPublisher,
    PaymentEventPublisherRef,
    kafka_runtime_from_bootstrap,
)
from app.postgres import PostgresPaymentRepository
from app.repository import PaymentRepository
from app.store import PaymentStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(frozen=True, slots=True)
class AppResources:
    repository: PaymentRepository
    event_publisher: PaymentEventPublisherRef
    engine: AsyncEngine | None = None
    kafka_bootstrap_servers: str = ""


def resources_from_env() -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    if database_url == "":
        return AppResources(
            repository=PaymentStore(),
            event_publisher=PaymentEventPublisherRef(NoopPaymentEventPublisher()),
            kafka_bootstrap_servers=kafka_bootstrap_servers,
        )

    engine = create_async_engine(
        _async_database_url(database_url),
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=0,
        pool_timeout=5.0,
        pool_recycle=1800,
        connect_args={"timeout": 3.0},
    )
    session_factory = async_sessionmaker(engine, expire_on_commit=False)
    return AppResources(
        repository=PostgresPaymentRepository(session_factory),
        event_publisher=PaymentEventPublisherRef(NoopPaymentEventPublisher()),
        engine=engine,
        kafka_bootstrap_servers=kafka_bootstrap_servers,
    )


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        runtime: KafkaRuntime | None = None
        try:
            if resources.kafka_bootstrap_servers != "":
                runtime = kafka_runtime_from_bootstrap(
                    resources.repository,
                    resources.kafka_bootstrap_servers,
                )
                if runtime.publisher is not None:
                    resources.event_publisher.replace(runtime.publisher)
            if runtime is not None and runtime.publisher is not None:
                await runtime.publisher.start()
            if runtime is not None and runtime.order_created_consumer is not None:
                await runtime.order_created_consumer.start()
            async with anyio.create_task_group() as task_group:
                if runtime is not None and runtime.order_created_consumer is not None:
                    task_group.start_soon(runtime.order_created_consumer.run)
                yield
                task_group.cancel_scope.cancel()
        finally:
            if runtime is not None and runtime.order_created_consumer is not None:
                await runtime.order_created_consumer.stop()
            if runtime is not None and runtime.publisher is not None:
                await runtime.publisher.stop()
            if resources.engine is not None:
                await resources.engine.dispose()

    return lifespan


async def database_schema_is_current(engine: AsyncEngine) -> bool:
    async with engine.connect() as connection:
        version_table = (
            await connection.execute(text("SELECT to_regclass('alembic_version')"))
        ).scalar_one()
        if version_table is None:
            return False
        version = (
            await connection.execute(text("SELECT version_num FROM alembic_version"))
        ).scalar_one_or_none()
    return version == "20260714_01"


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    return database_url
