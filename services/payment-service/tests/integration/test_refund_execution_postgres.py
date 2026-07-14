import os
from typing import Final

import pytest
from contracts import RefundRequestedEvent
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.ledger import RefundRecord
from app.records import ProcessedEventRecord
from app.refund_postgres import PostgresRefundRepository
from tests.integration.payment_outbox_support import postgres_schema
from tests.integration.refund_support import refund_requested_event, seed_payment_rows

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


@pytest.mark.anyio
async def test_duplicate_refund_request_creates_one_full_refund_for_approved_payment() -> (
    None
):
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        event = refund_requested_event()

        # When
        first_created = await repository.record_refund_requested(event)
        duplicate_created = await repository.record_refund_requested(event)

        # Then
        assert first_created is True
        assert duplicate_created is False
        async with session_factory() as session:
            refund = (await session.execute(select(RefundRecord))).scalar_one()
            inbox_count = int(
                (
                    await session.execute(
                        select(func.count()).select_from(ProcessedEventRecord),
                    )
                ).scalar_one(),
            )
        assert (
            refund.id,
            refund.order_id,
            refund.payment_id,
            refund.amount,
            refund.status,
            refund.attempts,
            inbox_count,
        ) == (
            "refund-001",
            "order-refund",
            "payment-refund",
            50000,
            "REQUESTED",
            0,
            1,
        )


@pytest.mark.anyio
@pytest.mark.parametrize(
    "event",
    [
        refund_requested_event().model_copy(
            update={"paymentId": "payment-missing"},
        ),
        refund_requested_event().model_copy(
            update={
                "refundId": "refund-failed-payment",
                "orderId": "order-failed",
                "paymentId": "payment-failed",
                "userId": "user-failed",
                "sourceId": "order-failed",
            },
        ),
        refund_requested_event().model_copy(
            update={"refundId": "refund-wrong-order", "orderId": "order-other"},
        ),
        refund_requested_event().model_copy(
            update={"refundId": "refund-wrong-user", "userId": "user-other"},
        ),
        refund_requested_event().model_copy(
            update={"refundId": "refund-partial", "amount": 49999},
        ),
        refund_requested_event().model_copy(
            update={
                "refundId": "refund-source-mismatch",
                "sourceId": "other-source",
            },
        ),
        refund_requested_event().model_copy(update={"refundId": "r" * 65}),
    ],
    ids=[
        "missing-payment",
        "unapproved-payment",
        "mismatched-order",
        "mismatched-user",
        "partial-amount",
        "mismatched-source",
        "oversized-refund-id",
    ],
)
async def test_invalid_refund_request_has_no_ledger_or_inbox_side_effect(
    event: RefundRequestedEvent,
) -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)

        # When
        created = await repository.record_refund_requested(event)

        # Then
        assert created is False
        async with session_factory() as session:
            counts = (
                await session.execute(
                    select(
                        select(func.count())
                        .select_from(RefundRecord)
                        .scalar_subquery(),
                        select(func.count())
                        .select_from(ProcessedEventRecord)
                        .scalar_subquery(),
                    ),
                )
            ).one()
        assert counts == (0, 0)


@pytest.mark.anyio
async def test_cross_combined_existing_refund_identifiers_are_rejected() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        first = refund_requested_event()
        second = first.model_copy(
            update={
                "eventId": "evt-refund-other",
                "refundId": "refund-other",
                "orderId": "order-other",
                "paymentId": "payment-other",
                "sourceId": "order-other",
            },
        )
        assert await repository.record_refund_requested(first) is True
        assert await repository.record_refund_requested(second) is True
        cross_combined = second.model_copy(
            update={
                "eventId": "evt-refund-cross-combined",
                "refundId": first.refundId,
            },
        )

        # When
        created = await repository.record_refund_requested(cross_combined)

        # Then
        assert created is False
        async with session_factory() as session:
            counts = (
                await session.execute(
                    select(
                        select(func.count())
                        .select_from(RefundRecord)
                        .scalar_subquery(),
                        select(func.count())
                        .select_from(ProcessedEventRecord)
                        .scalar_subquery(),
                    ),
                )
            ).one()
        assert counts == (2, 2)
