import json
import logging
import os
from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from typing import Final

import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import async_sessionmaker

from app.messaging import KafkaMessage, PaymentConsumer
from app.postgres import PostgresOrderRepository
from app.store import OrderCreated
from tests.integration.test_order_outbox_postgres import (
    _create_command,
    _postgres_schema,
)

ORDER_TEST_DATABASE_URL: Final = "ORDER_TEST_DATABASE_URL"


@dataclass(slots=True)  # noqa: MUTABLE_OK
class RawKafkaMessage:
    """Mutable SDK-shaped Kafka message used at the consumer protocol boundary."""

    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


class SingleMessageConsumer:
    def __init__(self, message: KafkaMessage) -> None:
        self._message = message
        self.commit_calls = 0

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self.commit_calls += 1

    async def _messages(self) -> AsyncIterator[KafkaMessage]:
        yield self._message

    def __aiter__(self) -> AsyncIterator[KafkaMessage]:
        return self._messages()


@pytest.mark.anyio
@pytest.mark.parametrize("oversized_field", ("orderId", "paymentId"))
async def test_oversized_payment_identifier_is_acknowledged_without_database_write(
    oversized_field: str,
    caplog: pytest.LogCaptureFixture,
) -> None:
    # Given
    database_url = os.environ[ORDER_TEST_DATABASE_URL]
    async with _postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresOrderRepository(session_factory)
        created = await repository.create_order(
            _create_command(f"invalid-{oversized_field.lower()}"),
        )
        assert isinstance(created, OrderCreated)
        event_id = f"evt-invalid-{oversized_field.lower()}"
        payload = {
            "eventId": event_id,
            "eventType": "payment.approved",
            "userId": created.order.userId,
            "sourceId": "payment-service",
            "occurredAt": "2026-07-14T12:00:00Z",
            "producer": "payment-service",
            "orderId": created.order.id,
            "paymentId": "p" * 64,
            "amount": created.order.amount,
            oversized_field: "x" * 65,
        }
        consumer_client = SingleMessageConsumer(
            RawKafkaMessage(
                topic="payment.approved",
                partition=0,
                offset=1,
                headers=None,
                value=json.dumps(payload).encode(),
            ),
        )
        consumer = PaymentConsumer(consumer_client, repository)

        # When
        with caplog.at_level(logging.WARNING, logger="app.messaging"):
            await consumer.run()

        # Then
        async with session_factory() as session:
            persisted = (
                await session.execute(
                    text(
                        """
                        SELECT o.status, o.payment_id,
                            (SELECT count(*) FROM processed_events
                             WHERE event_id = :event_id) AS inbox_count,
                            (SELECT count(*) FROM outbox_events
                             WHERE aggregate_id = :order_id) AS outbox_count
                        FROM orders AS o
                        WHERE o.id = :order_id
                        """,
                    ),
                    {"event_id": event_id, "order_id": created.order.id},
                )
            ).one()
        assert consumer_client.commit_calls == 1
        assert persisted == ("PENDING_PAYMENT", None, 0, 1)
        assert caplog.messages == ["discarded invalid payment.approved event"]
        assert "x" * 65 not in caplog.text
