import os
from collections.abc import AsyncIterator, Sequence
from datetime import datetime
from typing import Final

import pytest
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.ledger import RefundRecord
from app.messaging import KafkaMessage
from app.records import ProcessedEventRecord
from app.refund_messaging import RefundRequestedConsumer
from app.refund_postgres import PostgresRefundRepository
from tests.integration.payment_outbox_support import postgres_schema
from tests.integration.refund_support import refund_requested_event, seed_payment_rows
from tests.integration.test_payment_inbox_postgres import FakeKafkaMessage

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


class CommitCheckingConsumer:
    def __init__(
        self,
        messages: Sequence[KafkaMessage],
        session_factory: async_sessionmaker[AsyncSession],
    ) -> None:
        self._messages = messages
        self._session_factory = session_factory
        self.commit_snapshots: list[tuple[int, int]] = []

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        async with self._session_factory() as session:
            snapshot = (
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
        self.commit_snapshots.append((int(snapshot[0]), int(snapshot[1])))

    async def _iter_messages(self) -> AsyncIterator[KafkaMessage]:
        for message in self._messages:
            yield message

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._iter_messages()


@pytest.mark.anyio
async def test_refund_consumer_commits_offsets_after_atomic_inbox_and_ledger() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        valid = refund_requested_event()
        partial = refund_requested_event().model_copy(
            update={
                "eventId": "evt-refund-partial",
                "refundId": "refund-partial",
                "amount": 49999,
            },
        )
        consumer = CommitCheckingConsumer(
            [
                _message(valid.model_dump_json().encode(), offset=1),
                _message(valid.model_dump_json().encode(), offset=2),
                _message(partial.model_dump_json().encode(), offset=3),
            ],
            session_factory,
        )

        # When
        await RefundRequestedConsumer(consumer, repository).run()

        # Then
        assert consumer.commit_snapshots == [(1, 1), (1, 1), (1, 1)]


@pytest.mark.anyio
async def test_refund_consumer_commits_poison_messages_without_side_effects() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        await seed_payment_rows(engine)
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresRefundRepository(session_factory, max_attempts=3)
        base = refund_requested_event()
        poison_events = [
            base.model_copy(
                update={
                    "eventId": "evt-refund-nul-reason",
                    "reason": "customer\x00request",
                },
            ),
            base.model_copy(
                update={
                    "eventId": "evt-refund-nul-event\x00id",
                },
            ),
            base.model_copy(
                update={
                    "eventId": "evt-refund-naive-time",
                    "occurredAt": datetime(2026, 7, 14, 12, 2),
                },
            ),
            base.model_copy(
                update={
                    "eventId": "evt-refund-empty-id",
                    "refundId": "",
                },
            ),
        ]
        messages = [
            _message(event.model_dump_json().encode(), offset=offset)
            for offset, event in enumerate(poison_events, start=1)
        ]
        messages.append(_message(base.model_dump_json().encode(), offset=5))
        consumer = CommitCheckingConsumer(messages, session_factory)

        # When
        await RefundRequestedConsumer(consumer, repository).run()

        # Then
        assert consumer.commit_snapshots == [
            (0, 0),
            (0, 0),
            (0, 0),
            (0, 0),
            (1, 1),
        ]


def _message(value: bytes, offset: int) -> FakeKafkaMessage:
    return FakeKafkaMessage(
        topic="refund.requested",
        partition=0,
        offset=offset,
        headers=None,
        value=value,
    )
