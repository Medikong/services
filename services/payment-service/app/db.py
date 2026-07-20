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
from sqlalchemy import text
import sqlalchemy.ext.asyncio as sqlalchemy_asyncio
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
)

from app.kafka_workers import (
    run_order_created_consumer_worker,
    run_outbox_worker,
    run_refund_worker,
    run_refund_requested_consumer_worker,
)
from app.metrics import PaymentMetrics
from app.messaging import KafkaRuntimeConfig, kafka_runtime_from_config
from app.postgres import PostgresPaymentRepository
from app.refund_messaging import refund_requested_consumer_factory
from app.refund_postgres import PostgresRefundRepository
from app.refund_worker import RefundWorker
from app.refunds import MockRefundProvider, RefundRequestRepository
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
    refund_repository: RefundRequestRepository | None = None
    refund_worker: RefundWorker | None = None


class RefundConfigurationError(RuntimeError):
    pass


def resources_from_env(metrics: PaymentMetrics | None = None) -> AppResources:
    database_url = os.getenv("DATABASE_URL", "")
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    if database_url == "":
        return AppResources(
            repository=PaymentStore(),
            kafka_bootstrap_servers=kafka_bootstrap_servers,
            metrics=metrics,
        )

    instrument_sqlalchemy()
    engine = sqlalchemy_asyncio.create_async_engine(
        _async_database_url(database_url),
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=0,
        pool_timeout=5.0,
        pool_recycle=1800,
        connect_args={"timeout": 3.0},
    )
    instrument_sqlalchemy_pool_events(engine.sync_engine)
    session_factory = async_sessionmaker(engine, expire_on_commit=False)
    refund_repository = PostgresRefundRepository(
        session_factory,
        max_attempts=_refund_max_attempts(),
    )
    return AppResources(
        repository=PostgresPaymentRepository(session_factory),
        engine=engine,
        session_factory=session_factory,
        kafka_bootstrap_servers=kafka_bootstrap_servers,
        metrics=metrics,
        refund_repository=refund_repository,
        refund_worker=RefundWorker(refund_repository, MockRefundProvider()),
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
        refund_consumer_factory = refund_requested_consumer_factory(
            resources.kafka_bootstrap_servers,
            resources.refund_repository,
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
                if refund_consumer_factory is not None:
                    task_group.start_soon(
                        run_refund_requested_consumer_worker,
                        refund_consumer_factory,
                    )
                if resources.refund_worker is not None:
                    task_group.start_soon(run_refund_worker, resources.refund_worker)
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


def _refund_max_attempts() -> int:
    raw_value = os.getenv("REFUND_MAX_ATTEMPTS", "3")
    try:
        max_attempts = int(raw_value)
    except ValueError as error:
        raise RefundConfigurationError(
            f"REFUND_MAX_ATTEMPTS must be an integer, got {raw_value!r}",
        ) from error
    if max_attempts < 1:
        raise RefundConfigurationError(
            f"REFUND_MAX_ATTEMPTS must be positive, got {max_attempts}",
        )
    return max_attempts
