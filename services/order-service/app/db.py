import os
from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager
from dataclasses import dataclass, field

import anyio
from fastapi import FastAPI
from observability import (
    instrument_sqlalchemy,
    instrument_sqlalchemy_pool_events,
)
import sqlalchemy.ext.asyncio as sqlalchemy_asyncio
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
)

from app.messaging import (
    KafkaRuntime,
    KafkaRuntimeConfig,
    kafka_runtime_from_config,
)
from app.kafka_workers import run_outbox_worker, run_payment_consumer_worker
from app.metrics import OrderMetrics
from app.expiry import OrderExpiryWorker
from app.order_config import order_payment_policy_from_env
from app.postgres import PostgresOrderRepository
from app.repository import OrderRepository
from app.store import OrderStore

type FastAPILifespan = Callable[[FastAPI], AbstractAsyncContextManager[None]]


@dataclass(slots=True)  # noqa: MUTABLE_OK
class AppResources:
    """Resources mutated by lifespan after the async event loop starts."""

    repository: OrderRepository
    engine: AsyncEngine | None = None
    session_factory: async_sessionmaker[AsyncSession] | None = None
    kafka_bootstrap_servers: str = ""
    kafka_runtime: KafkaRuntime | None = None
    metrics: OrderMetrics = field(
        default_factory=lambda: OrderMetrics("order-service", "unknown", "unknown"),
    )
    expiry_worker: OrderExpiryWorker | None = None


def resources_from_env(metrics: OrderMetrics | None = None) -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    order_metrics = metrics or OrderMetrics("order-service", "unknown", "unknown")
    if database_url == "":
        repository = OrderStore()
        return AppResources(
            repository=repository,
            kafka_bootstrap_servers=kafka_bootstrap_servers,
            metrics=order_metrics,
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
    payment_policy = order_payment_policy_from_env()
    repository = PostgresOrderRepository(
        session_factory,
        policy=payment_policy,
        metrics=order_metrics,
    )
    return AppResources(
        repository=repository,
        engine=engine,
        session_factory=session_factory,
        kafka_bootstrap_servers=kafka_bootstrap_servers,
        metrics=order_metrics,
        expiry_worker=OrderExpiryWorker(repository, payment_policy.clock),
    )


def lifespan_for(resources: AppResources) -> FastAPILifespan:
    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        runtime = resources.kafka_runtime
        try:
            if runtime is None and resources.kafka_bootstrap_servers != "":
                runtime = kafka_runtime_from_config(
                    KafkaRuntimeConfig(
                        bootstrap_servers=resources.kafka_bootstrap_servers,
                        repository=resources.repository,
                        session_factory=resources.session_factory,
                        metrics=resources.metrics,
                    ),
                )
                resources.kafka_runtime = runtime
            async with anyio.create_task_group() as task_group:
                if runtime is not None and runtime.payment_consumer_factory is not None:
                    task_group.start_soon(
                        run_payment_consumer_worker,
                        runtime.payment_consumer_factory,
                    )
                if runtime is not None and runtime.outbox_worker_factory is not None:
                    task_group.start_soon(
                        run_outbox_worker,
                        runtime.outbox_worker_factory,
                    )
                if resources.expiry_worker is not None:
                    task_group.start_soon(resources.expiry_worker.run)
                yield
                task_group.cancel_scope.cancel()
        finally:
            if resources.engine is not None:
                await resources.engine.dispose()

    return lifespan


def _async_database_url(database_url: str) -> str:
    if database_url.startswith("postgresql+psycopg://"):
        return database_url.replace("postgresql+psycopg://", "postgresql+asyncpg://", 1)
    if database_url.startswith("postgresql://"):
        return database_url.replace("postgresql://", "postgresql+asyncpg://", 1)
    return database_url
