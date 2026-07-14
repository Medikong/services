import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from typing import Final, assert_never
from uuid import uuid4

import anyio
import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)

from app.models import IdempotencyKey, OrderId, Payment, PaymentMethod, UserId
from app.postgres import Base, PostgresPaymentRepository
from app.store import (
    FailPaymentCommand,
    FailPaymentResult,
    PaymentAlreadyFailed,
    PaymentFailed,
    PaymentIdempotencyConflict,
    PaymentOrderMismatch,
    PaymentOrderNotFound,
    PaymentTerminalConflict,
)
from contracts import OrderCreatedEvent

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


@pytest.mark.anyio
async def test_fail_mock_payment_replays_one_failure_when_two_sessions_race() -> None:
    # Given
    database_url = os.getenv(PAYMENT_TEST_DATABASE_URL)
    if database_url is None or database_url == "":
        pytest.skip(f"{PAYMENT_TEST_DATABASE_URL} is not set")

    schema_name = f"payment_idempotency_{uuid4().hex}"
    order_created = OrderCreatedEvent(
        eventId="evt-payment-idempotency",
        userId="user-payment-idempotency",
        sourceId="order-payment-idempotency",
        occurredAt=datetime(2026, 7, 13, 12, 0, tzinfo=UTC),
        producer="payment-service-test",
        orderId="order-payment-idempotency",
        dropId="drop-payment-idempotency",
        productId="product-payment-idempotency",
        quantity=1,
        amount=50000,
        idempotencyKey="order-payment-idempotency",
    )
    command = FailPaymentCommand(
        user_id=UserId(order_created.userId),
        order_id=OrderId(order_created.orderId),
        amount=order_created.amount,
        method=PaymentMethod.MOCK_CARD,
        idempotency_key=IdempotencyKey("payment-failure-idempotency"),
        reason="card_declined",
    )

    async with _postgres_schema(database_url, schema_name) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        await _create_schema_tables(engine)
        repository = PostgresPaymentRepository(session_factory)
        await repository.record_order_created(order_created)

        # When
        results = await _fail_concurrently(
            session_factory=session_factory,
            command=command,
        )

        # Then
        failed_payment, replayed_payment = _one_failed_and_one_replayed(results)
        assert failed_payment.id == replayed_payment.id
        assert (
            await _matching_payment_count(
                session_factory=session_factory,
                payment=failed_payment,
                command=command,
            )
            == 1
        )


@asynccontextmanager
async def _postgres_schema(
    database_url: str,
    schema_name: str,
) -> AsyncIterator[AsyncEngine]:
    admin_engine = create_async_engine(database_url)
    engine = create_async_engine(
        database_url,
        connect_args={"server_settings": {"search_path": schema_name}},
    )
    try:
        async with admin_engine.begin() as connection:
            await connection.execute(text(f"CREATE SCHEMA {schema_name}"))
        yield engine
    finally:
        await engine.dispose()
        async with admin_engine.begin() as connection:
            await connection.execute(
                text(f"DROP SCHEMA IF EXISTS {schema_name} CASCADE"),
            )
        await admin_engine.dispose()


async def _create_schema_tables(engine: AsyncEngine) -> None:
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)


async def _fail_concurrently(
    *,
    session_factory: async_sessionmaker[AsyncSession],
    command: FailPaymentCommand,
) -> list[FailPaymentResult]:
    results: list[FailPaymentResult] = []
    start = anyio.Event()
    ready = anyio.Event()
    ready_lock = anyio.Lock()
    ready_count = 0

    async def fail_payment() -> None:
        nonlocal ready_count
        repository = PostgresPaymentRepository(session_factory)
        async with ready_lock:
            ready_count += 1
            if ready_count == 2:
                ready.set()
        await start.wait()
        results.append(await repository.fail_mock_payment(command))

    async with anyio.create_task_group() as task_group:
        task_group.start_soon(fail_payment)
        task_group.start_soon(fail_payment)
        await ready.wait()
        start.set()

    return results


def _one_failed_and_one_replayed(
    results: list[FailPaymentResult],
) -> tuple[Payment, Payment]:
    failed_payment: Payment | None = None
    replayed_payment: Payment | None = None

    for result in results:
        match result:
            case PaymentFailed(payment=payment):
                if failed_payment is not None:
                    pytest.fail("expected exactly one PaymentFailed result")
                failed_payment = payment
            case PaymentAlreadyFailed(payment=payment):
                if replayed_payment is not None:
                    pytest.fail("expected exactly one PaymentAlreadyFailed result")
                replayed_payment = payment
            case (
                PaymentOrderNotFound()
                | PaymentOrderMismatch()
                | PaymentIdempotencyConflict()
                | PaymentTerminalConflict()
            ):
                pytest.fail(f"unexpected payment result: {type(result).__name__}")
            case unreachable:
                assert_never(unreachable)

    if failed_payment is None:
        pytest.fail("expected one PaymentFailed result")
    if replayed_payment is None:
        pytest.fail("expected one PaymentAlreadyFailed result")

    return failed_payment, replayed_payment


async def _matching_payment_count(
    *,
    session_factory: async_sessionmaker[AsyncSession],
    payment: Payment,
    command: FailPaymentCommand,
) -> int:
    async with session_factory() as session:
        result = await session.execute(
            text(
                """
                SELECT count(*)
                FROM payments
                WHERE id = :payment_id
                  AND order_id = :order_id
                  AND user_id = :user_id
                  AND amount = :amount
                  AND method = :method
                  AND status = 'FAILED'
                  AND idempotency_key = :idempotency_key
                  AND approved_at IS NULL
                  AND failed_at IS NOT NULL
                  AND failure_reason = :failure_reason
                """,
            ),
            {
                "payment_id": payment.id,
                "order_id": command.order_id,
                "user_id": command.user_id,
                "amount": command.amount,
                "method": command.method.value,
                "idempotency_key": command.idempotency_key,
                "failure_reason": command.reason,
            },
        )
        return int(result.scalar_one())
