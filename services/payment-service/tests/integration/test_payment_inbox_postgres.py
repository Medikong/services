import logging
import os
from collections.abc import AsyncIterator, Sequence
from datetime import UTC, datetime
from typing import Final

import orjson
import pytest
from contracts import OrderCreatedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.messaging import KafkaMessage, OrderCreatedConsumer
from app.postgres import PostgresPaymentRepository
from tests.integration.payment_outbox_support import postgres_schema

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


class FakeKafkaMessage:
    def __init__(
        self,
        *,
        topic: str,
        partition: int,
        offset: int,
        headers: Sequence[tuple[str | bytes, bytes]] | None,
        value: bytes | None,
    ) -> None:
        self.topic = topic
        self.partition = partition
        self.offset = offset
        self.headers = headers
        self.value = value


class DatabaseCheckingConsumer:
    def __init__(
        self,
        messages: Sequence[KafkaMessage],
        session_factory: async_sessionmaker[AsyncSession],
        event_id: str,
    ) -> None:
        self._messages_to_yield: Sequence[KafkaMessage] = messages
        self._session_factory = session_factory
        self._event_id = event_id
        self.inbox_count_at_commit = 0
        self.commit_count: int = 0

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self.commit_count += 1
        async with self._session_factory() as session:
            self.inbox_count_at_commit = int(
                (
                    await session.execute(
                        text(
                            "SELECT count(*) FROM processed_events "
                            "WHERE event_id = :event_id",
                        ),
                        {"event_id": self._event_id},
                    )
                ).scalar_one(),
            )

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        for message in self._messages_to_yield:
            yield message

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._messages()


@pytest.mark.anyio
async def test_order_id_boundary_preserves_inbox_and_offset_progress(
    caplog: pytest.LogCaptureFixture,
) -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresPaymentRepository(session_factory)
        event = _order_created().model_copy(
            update={"orderId": "o" * 64, "sourceId": "o" * 64}
        )
        invalid_order_id = "o" * 65
        sensitive_correlation = "sensitive-" + ("x" * 256)
        invalid_payload = orjson.dumps(
            event.model_dump(mode="json")
            | {
                "eventId": "evt-order-id-65",
                "orderId": invalid_order_id,
            }
        )
        consumer = DatabaseCheckingConsumer(
            [
                FakeKafkaMessage(
                    topic="order.created",
                    partition=0,
                    offset=1,
                    headers=None,
                    value=event.model_dump_json().encode(),
                ),
                FakeKafkaMessage(
                    topic="order.created",
                    partition=0,
                    offset=2,
                    headers=None,
                    value=event.model_dump_json().encode(),
                ),
                FakeKafkaMessage(
                    topic="order.created",
                    partition=0,
                    offset=3,
                    headers=[("correlation_id", sensitive_correlation.encode("utf-8"))],
                    value=invalid_payload,
                ),
            ],
            session_factory,
            event.eventId,
        )

        # When
        with caplog.at_level(logging.INFO):
            await OrderCreatedConsumer(consumer, repository).run()

        # Then
        assert consumer.inbox_count_at_commit == 1
        assert consumer.commit_count == 3
        async with session_factory() as session:
            counts = (
                await session.execute(
                    text(
                        "SELECT (SELECT count(*) FROM known_orders), "
                        "(SELECT count(*) FROM processed_events)",
                    ),
                )
            ).one()
        assert counts == (1, 1)
        records = [
            record
            for record in caplog.records
            if record.name == "app.messaging"
            or (
                record.name == "payment-service"
                and '"messaging.kafka.message.offset":3' in record.getMessage()
            )
        ]
        assert len(records) == 1
        assert records[0].name == "app.messaging"
        assert records[0].args == (0, 3)
        assert len(records[0].getMessage()) <= 128
        assert invalid_order_id not in records[0].getMessage()
        assert sensitive_correlation not in records[0].getMessage()


@pytest.mark.anyio
async def test_event_id_collision_is_acked_without_projecting_payload() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresPaymentRepository(session_factory)
        original = _order_created()
        await repository.record_order_created(original)
        collision = original.model_copy(
            update={
                "orderId": "order-inbox-collision",
                "sourceId": "order-inbox-collision",
            }
        )
        message = FakeKafkaMessage(
            topic="order.created",
            partition=0,
            offset=2,
            headers=None,
            value=collision.model_dump_json().encode(),
        )
        consumer = DatabaseCheckingConsumer(
            [message],
            session_factory,
            original.eventId,
        )

        # When
        await OrderCreatedConsumer(consumer, repository).run()

        # Then
        assert consumer.inbox_count_at_commit == 1
        async with session_factory() as session:
            projected = int(
                (
                    await session.execute(
                        text(
                            "SELECT count(*) FROM known_orders "
                            "WHERE order_id = 'order-inbox-collision'",
                        )
                    )
                ).scalar_one()
            )
        assert projected == 0


def _order_created() -> OrderCreatedEvent:
    return OrderCreatedEvent(
        eventId="evt-inbox-replay",
        userId="user-inbox-replay",
        sourceId="order-inbox-replay",
        occurredAt=datetime(2026, 7, 14, 12, 0, tzinfo=UTC),
        producer="order-service",
        orderId="order-inbox-replay",
        dropId="drop-001",
        productId="product-001",
        quantity=1,
        amount=50000,
        idempotencyKey="order-inbox-replay",
    )
