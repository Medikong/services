import os
from datetime import UTC, datetime, timedelta
from typing import Final

import anyio
import pytest
from contracts import OrderCreatedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.metrics import PaymentMetrics
from app.models import IdempotencyKey, OrderId, PaymentMethod, UserId
from app.outbox import MAX_RETRY_DELAY, OutboxMessage, OutboxRelay, retry_delay
from app.postgres import PostgresPaymentRepository
from app.store import ApprovePaymentCommand, PaymentApproved
from tests.integration.payment_outbox_support import postgres_schema

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


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


def test_retry_delay_uses_bounded_exponential_backoff() -> None:
    # Given
    attempts = (1, 2, 9, 10)

    # When
    delays = tuple(retry_delay(attempt) for attempt in attempts)

    # Then
    assert delays == (
        timedelta(seconds=1),
        timedelta(seconds=2),
        timedelta(seconds=256),
        MAX_RETRY_DELAY,
    )


@pytest.mark.anyio
async def test_pending_event_survives_broker_failure_and_recovery() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_approved_payment(session_factory, "broker-recovery")
        failed_at = datetime(2026, 7, 14, 12, 0, tzinfo=UTC)
        failed_relay = OutboxRelay(session_factory, FailingPublisher(), _metrics())

        # When
        failed_attempt = await failed_relay.relay_once(failed_at)
        publisher = RecordingPublisher()
        recovered_attempt = await OutboxRelay(
            session_factory,
            publisher,
            _metrics(),
        ).relay_once(failed_at + timedelta(seconds=1))

        # Then
        assert (failed_attempt, recovered_attempt) == (True, True)
        assert len(publisher.deliveries) == 1
        delivery = publisher.deliveries[0]
        assert delivery.event_id == delivery.payload["eventId"]
        assert delivery.topic == "payment.approved"
        assert delivery.message_key == "order-broker-recovery"
        state = await _outbox_state(session_factory)
        assert state.attempts == 1
        assert state.published_at is not None


@pytest.mark.anyio
async def test_competing_relays_use_skip_locked() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_approved_payment(session_factory, "relay-lock")
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
async def test_tenth_failure_dead_letters_event() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_approved_payment(session_factory, "dead-letter")
        relay = OutboxRelay(session_factory, FailingPublisher(), _metrics())
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


@pytest.mark.anyio
async def test_crash_after_send_retries_same_event_id() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_approved_payment(session_factory, "send-crash")
        crashing_publisher = CrashAfterSendPublisher()

        # When
        with pytest.raises(SimulatedProcessCrash):
            await OutboxRelay(
                session_factory,
                crashing_publisher,
                _metrics(),
            ).relay_once()
        restarted_publisher = RecordingPublisher()
        await OutboxRelay(
            session_factory,
            restarted_publisher,
            _metrics(),
        ).relay_once()

        # Then
        assert crashing_publisher.deliveries[0].event_id == (
            restarted_publisher.deliveries[0].event_id
        )
        assert (await _outbox_state(session_factory)).published_at is not None


async def _create_approved_payment(
    session_factory: async_sessionmaker[AsyncSession],
    suffix: str,
) -> None:
    repository = PostgresPaymentRepository(session_factory)
    event = OrderCreatedEvent(
        eventId=f"evt-{suffix}",
        userId=f"user-{suffix}",
        sourceId=f"order-{suffix}",
        occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
        producer="order-service",
        orderId=f"order-{suffix}",
        dropId="drop-001",
        productId="product-001",
        quantity=1,
        amount=50000,
        idempotencyKey=f"order-{suffix}",
    )
    await repository.record_order_created(event)
    result = await repository.approve_mock_payment(
        ApprovePaymentCommand(
            user_id=UserId(event.userId),
            order_id=OrderId(event.orderId),
            amount=event.amount,
            method=PaymentMethod.MOCK_CARD,
            idempotency_key=IdempotencyKey(f"payment-{suffix}"),
        ),
    )
    assert isinstance(result, PaymentApproved)


async def _outbox_state(session_factory: async_sessionmaker[AsyncSession]):
    async with session_factory() as session:
        return (
            await session.execute(
                text(
                    "SELECT attempts, last_error, published_at, dead_lettered_at "
                    "FROM outbox_events ORDER BY occurred_at LIMIT 1",
                ),
            )
        ).one()


def _metrics() -> PaymentMetrics:
    return PaymentMetrics("payment-service", "test", "test")
