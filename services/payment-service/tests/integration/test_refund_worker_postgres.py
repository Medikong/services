import os
from datetime import timedelta
from typing import Final

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.ledger import RefundRecord
from app.records import OutboxEventRecord
from app.refund_postgres import PostgresRefundRepository
from app.refund_worker import RefundWorker
from app.refunds import (
    MockRefundProvider,
    RefundAttempt,
    RefundProviderFailed,
    RefundProviderResult,
)
from tests.integration.payment_outbox_support import postgres_schema
from tests.integration.refund_support import (
    OCCURRED_AT,
    refund_requested_event,
    seed_payment_rows,
)

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"
PROCESSING_LEASE: Final = timedelta(minutes=5)


class PermanentFailureProvider:
    async def refund(self, attempt: RefundAttempt) -> RefundProviderResult:
        return RefundProviderFailed(reason="x" * 700)


@pytest.mark.anyio
async def test_process_restart_reclaims_stale_processing_attempt() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        _ = await repository.record_refund_requested(refund_requested_event())
        claimed_at = OCCURRED_AT + timedelta(seconds=1)
        abandoned_attempt = await repository.claim_due_refund(claimed_at)
        assert abandoned_attempt is not None
        restarted_worker = RefundWorker(
            PostgresRefundRepository(session_factory, max_attempts=3),
            MockRefundProvider(),
        )

        # When
        before_lease = await restarted_worker.process_once(
            claimed_at + PROCESSING_LEASE - timedelta(microseconds=1),
        )
        after_lease = await restarted_worker.process_once(
            claimed_at + PROCESSING_LEASE,
        )

        # Then
        assert before_lease is False
        assert after_lease is True
        async with session_factory() as session:
            completed = (await session.execute(select(RefundRecord))).scalar_one()
            outbox = (await session.execute(select(OutboxEventRecord))).scalar_one()
        assert (
            completed.status,
            completed.attempts,
            outbox.event_id,
        ) == (
            "COMPLETED",
            1,
            "evt-refund-completed-refund-001",
        )


@pytest.mark.anyio
async def test_process_restart_retries_same_refund_and_completes_once() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        _ = await repository.record_refund_requested(refund_requested_event())
        first_attempt_at = OCCURRED_AT + timedelta(seconds=1)

        # When
        first_worker = RefundWorker(repository, MockRefundProvider(frozenset({1})))
        first_processed = await first_worker.process_once(first_attempt_at)

        # Then
        assert first_processed is True
        async with session_factory() as session:
            first_state = (await session.execute(select(RefundRecord))).scalar_one()
            first_outbox_count = int(
                (
                    await session.execute(
                        select(func.count()).select_from(OutboxEventRecord),
                    )
                ).scalar_one(),
            )
        assert (
            first_state.id,
            first_state.status,
            first_state.attempts,
            first_state.last_error,
            first_state.next_attempt_at,
            first_state.completed_at,
            first_outbox_count,
        ) == (
            "refund-001",
            "FAILED",
            1,
            "injected provider failure at attempt 1",
            first_attempt_at + timedelta(seconds=1),
            None,
            0,
        )

        # When
        restarted_worker = RefundWorker(
            repository,
            MockRefundProvider(frozenset({1})),
        )
        restarted = await restarted_worker.process_once(
            first_attempt_at + timedelta(seconds=1),
        )

        # Then
        assert restarted is True
        async with session_factory() as session:
            completed = (await session.execute(select(RefundRecord))).scalar_one()
            outbox = (await session.execute(select(OutboxEventRecord))).scalar_one()
        assert (
            completed.id,
            completed.status,
            completed.attempts,
            completed.last_error,
            outbox.event_id,
            outbox.event_type,
            outbox.aggregate_id,
            outbox.payload["refundId"],
        ) == (
            "refund-001",
            "COMPLETED",
            2,
            None,
            "evt-refund-completed-refund-001",
            "refund.completed",
            "refund-001",
            "refund-001",
        )


@pytest.mark.anyio
async def test_permanent_failure_stops_at_limit_and_emits_one_failed_event() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        _ = await repository.record_refund_requested(refund_requested_event())
        worker = RefundWorker(repository, PermanentFailureProvider())
        first_attempt_at = OCCURRED_AT + timedelta(seconds=1)

        # When
        await worker.process_once(first_attempt_at)
        await worker.process_once(first_attempt_at + timedelta(seconds=1))
        await worker.process_once(first_attempt_at + timedelta(seconds=3))
        extra_processed = await worker.process_once(
            first_attempt_at + timedelta(days=1)
        )

        # Then
        assert extra_processed is False
        async with session_factory() as session:
            failed = (await session.execute(select(RefundRecord))).scalar_one()
            outbox_rows = (
                (await session.execute(select(OutboxEventRecord))).scalars().all()
            )
        assert (
            failed.status,
            failed.attempts,
            len(failed.last_error or ""),
            failed.next_attempt_at,
            failed.completed_at,
        ) == ("FAILED", 3, 500, None, None)
        assert len(outbox_rows) == 1
        assert (
            outbox_rows[0].event_id,
            outbox_rows[0].event_type,
            outbox_rows[0].payload["refundId"],
            len(str(outbox_rows[0].payload["reason"])),
        ) == (
            "evt-refund-failed-refund-001",
            "refund.failed",
            "refund-001",
            500,
        )
