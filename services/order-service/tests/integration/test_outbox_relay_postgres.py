import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime, timedelta
from typing import Final
from uuid import uuid4

import anyio
import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine, async_sessionmaker, create_async_engine

from app.metrics import OrderMetrics
from app.outbox import OutboxMessage, OutboxRelay
from app.postgres import Base, PostgresOrderRepository
from app.store import OrderCreated
from tests.integration.test_order_outbox_postgres import _create_command

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"


class RecordingPublisher:
    def __init__(self) -> None:
        self.deliveries: list[OutboxMessage] = []

    async def publish(self, message: OutboxMessage) -> None:
        self.deliveries.append(message)


class FailingPublisher:
    async def publish(self, message: OutboxMessage) -> None:
        raise RuntimeError("broker unavailable " + "x" * 1000)


class SimulatedProcessCrash(BaseException):
    pass


class CrashAfterSendPublisher(RecordingPublisher):
    async def publish(self, message: OutboxMessage) -> None:
        await super().publish(message)
        raise SimulatedProcessCrash


class BlockingPublisher(RecordingPublisher):
    def __init__(self, entered: anyio.Event, release: anyio.Event) -> None:
        super().__init__()
        self._entered = entered
        self._release = release

    async def publish(self, message: OutboxMessage) -> None:
        await super().publish(message)
        self._entered.set()
        await self._release.wait()


@pytest.mark.anyio
async def test_pending_event_survives_broker_failure_and_restart() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(_create_command("broker-restart"))
        assert isinstance(created, OrderCreated)
        failed_at = datetime(2026, 7, 14, 12, 0, tzinfo=UTC)
        metrics = _metrics()
        failed_relay = OutboxRelay(session_factory, FailingPublisher(), metrics)

        # When
        failed_attempt = await failed_relay.relay_once(failed_at)
        publisher = RecordingPublisher()
        restarted_relay = OutboxRelay(session_factory, publisher, metrics)
        recovered_attempt = await restarted_relay.relay_once(
            failed_at + timedelta(seconds=1),
        )

        # Then
        assert failed_attempt is True
        assert recovered_attempt is True
        assert [delivery.event_id for delivery in publisher.deliveries]
        state = await _outbox_state(session_factory)
        assert state.attempts == 1
        assert state.published_at is not None


@pytest.mark.anyio
async def test_competing_relays_do_not_claim_the_same_row() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(_create_command("relay-competition"))
        assert isinstance(created, OrderCreated)
        entered = anyio.Event()
        release = anyio.Event()
        second_done = anyio.Event()
        publisher = BlockingPublisher(entered, release)
        relays = (
            OutboxRelay(session_factory, publisher, _metrics()),
            OutboxRelay(session_factory, publisher, _metrics()),
        )
        results: list[bool] = []

        async def run_first() -> None:
            results.append(await relays[0].relay_once())

        async def run_second() -> None:
            results.append(await relays[1].relay_once())
            second_done.set()

        # When
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(run_first)
            await entered.wait()
            task_group.start_soon(run_second)
            await second_done.wait()
            release.set()

        # Then
        assert sorted(results) == [False, True]
        assert len(publisher.deliveries) == 1


@pytest.mark.anyio
async def test_tenth_failure_dead_letters_with_bounded_error_and_metric() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(_create_command("dead-letter"))
        assert isinstance(created, OrderCreated)
        metrics = _metrics()
        relay = OutboxRelay(session_factory, FailingPublisher(), metrics)
        started_at = datetime(2026, 7, 14, 12, 0, tzinfo=UTC)

        # When
        attempts = [
            await relay.relay_once(started_at + timedelta(minutes=10 * index))
            for index in range(10)
        ]

        # Then
        assert attempts == [True] * 10
        state = await _outbox_state(session_factory)
        assert state.attempts == 10
        assert state.dead_lettered_at is not None
        assert state.last_error is not None
        assert len(state.last_error) <= 500
        rendered_metrics = metrics.render()
        assert "order_outbox_relay_total{" in rendered_metrics
        assert 'outcome="dead_lettered"} 1' in rendered_metrics


@pytest.mark.anyio
async def test_crash_after_send_retries_the_same_event_id() -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(_create_command("send-crash"))
        assert isinstance(created, OrderCreated)
        crashing_publisher = CrashAfterSendPublisher()
        relay = OutboxRelay(session_factory, crashing_publisher, _metrics())

        # When
        with pytest.raises(SimulatedProcessCrash):
            await relay.relay_once()
        restarted_publisher = RecordingPublisher()
        restarted_relay = OutboxRelay(
            session_factory,
            restarted_publisher,
            _metrics(),
        )
        await restarted_relay.relay_once()

        # Then
        assert crashing_publisher.deliveries[0].event_id == (
            restarted_publisher.deliveries[0].event_id
        )
        assert (await _outbox_state(session_factory)).published_at is not None


@asynccontextmanager
async def _postgres_schema(database_url: str) -> AsyncIterator[AsyncEngine]:
    schema_name = f"outbox_relay_{uuid4().hex}"
    admin_engine = create_async_engine(database_url)
    engine = create_async_engine(
        database_url,
        connect_args={"server_settings": {"search_path": schema_name}},
    )
    try:
        async with admin_engine.begin() as connection:
            await connection.execute(text(f"CREATE SCHEMA {schema_name}"))
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)
        yield engine
    finally:
        await engine.dispose()
        async with admin_engine.begin() as connection:
            await connection.execute(
                text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE")
            )
        await admin_engine.dispose()


async def _outbox_state(session_factory: async_sessionmaker):
    async with session_factory() as session:
        result = await session.execute(
            text(
                "SELECT attempts, last_error, published_at, dead_lettered_at "
                "FROM outbox_events ORDER BY occurred_at LIMIT 1",
            ),
        )
        return result.one()


def _metrics() -> OrderMetrics:
    return OrderMetrics("order-service", "test", "test")
