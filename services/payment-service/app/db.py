import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass

import anyio
from fastapi import FastAPI
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from app.messaging import KafkaRuntimeConfig, kafka_runtime_from_config
from app.kafka_workers import run_order_created_consumer_worker, run_outbox_worker
from app.metrics import PaymentMetrics
from app.postgres import PostgresPaymentRepository
from app.repository import PaymentRepository
from app.store import PaymentStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)  # noqa: MUTABLE_OK
class AppResources:
    """Resources assembled before lifespan starts async Kafka workers."""

    repository: PaymentRepository
    engine: AsyncEngine | None = None
    session_factory: async_sessionmaker[AsyncSession] | None = None
    kafka_bootstrap_servers: str = ""
    metrics: PaymentMetrics | None = None


def resources_from_env(metrics: PaymentMetrics | None = None) -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    if database_url == "":
        return AppResources(
            repository=PaymentStore(),
            kafka_bootstrap_servers=kafka_bootstrap_servers,
            metrics=metrics,
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
        engine=engine,
        session_factory=session_factory,
        kafka_bootstrap_servers=kafka_bootstrap_servers,
        metrics=metrics,
    )


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        runtime = kafka_runtime_from_config(
            KafkaRuntimeConfig(
                bootstrap_servers=resources.kafka_bootstrap_servers,
                repository=resources.repository,
                session_factory=resources.session_factory,
                metrics=resources.metrics
                or PaymentMetrics("payment-service", "unknown", "unknown"),
            ),
        )
        try:
            async with anyio.create_task_group() as task_group:
                if runtime.order_created_consumer_factory is not None:
                    task_group.start_soon(
                        run_order_created_consumer_worker,
                        runtime.order_created_consumer_factory,
                    )
                if runtime.outbox_worker_factory is not None:
                    task_group.start_soon(
                        run_outbox_worker,
                        runtime.outbox_worker_factory,
                    )
                yield
                task_group.cancel_scope.cancel()
        finally:
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
    return version == "20260714_02"


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    return database_url
