import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass

import anyio
from fastapi import FastAPI
from observability import (
    instrument_sqlalchemy,
    instrument_sqlalchemy_pool_events,
)
import sqlalchemy.ext.asyncio as sqlalchemy_asyncio
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker

from app.messaging import KafkaRuntime, kafka_runtime_from_bootstrap
from app.metrics import NotificationMetrics
from app.postgres import PostgresNotificationRepository
from app.repository import NotificationRepository
from app.store import NotificationStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)  # noqa: MUTABLE_OK
class AppResources:
    repository: NotificationRepository
    notification_metrics: NotificationMetrics
    engine: AsyncEngine | None = None
    kafka_bootstrap_servers: str = ""
    kafka_runtime: KafkaRuntime | None = None


def resources_from_env(notification_metrics: NotificationMetrics) -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    if database_url == "":
        return AppResources(
            repository=NotificationStore(),
            notification_metrics=notification_metrics,
            kafka_bootstrap_servers=kafka_bootstrap_servers,
        )

    instrument_sqlalchemy()
    engine = sqlalchemy_asyncio.create_async_engine(
        _async_database_url(database_url),
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=0,
        pool_timeout=5.0,
        pool_recycle=1800,
    )
    instrument_sqlalchemy_pool_events(engine.sync_engine)
    session_factory = async_sessionmaker(engine, expire_on_commit=False)
    return AppResources(
        repository=PostgresNotificationRepository(session_factory),
        notification_metrics=notification_metrics,
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
                    resources.notification_metrics,
                )
                resources.kafka_runtime = runtime
            if (
                runtime is not None
                and runtime.notification_requested_consumer is not None
            ):
                await runtime.notification_requested_consumer.start()
            async with anyio.create_task_group() as task_group:
                if (
                    runtime is not None
                    and runtime.notification_requested_consumer is not None
                ):
                    task_group.start_soon(runtime.notification_requested_consumer.run)
                yield
                task_group.cancel_scope.cancel()
        finally:
            if (
                runtime is not None
                and runtime.notification_requested_consumer is not None
            ):
                await runtime.notification_requested_consumer.stop()
            if resources.engine is not None:
                await resources.engine.dispose()

    return lifespan


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    return database_url
