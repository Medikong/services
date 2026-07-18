import os
from datetime import UTC, datetime
from typing import Final

import anyio
import pytest
from contracts import OrderCreatedEvent
from httpx import ASGITransport, AsyncClient, Response
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.main import create_app
from app.postgres import PostgresPaymentRepository
from tests.integration.payment_outbox_support import postgres_schema

PAYMENT_TEST_DATABASE_URL: Final = "PAYMENT_TEST_DATABASE_URL"


@pytest.mark.anyio
async def test_approval_and_failure_requests_create_one_terminal_outbox() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresPaymentRepository(session_factory)
        order_created = _order_created("terminal-race")
        await repository.record_order_created(order_created)
        responses: list[Response] = []
        start = anyio.Event()
        ready = anyio.Event()
        ready_count = 0
        ready_lock = anyio.Lock()

        async with AsyncClient(
            transport=ASGITransport(app=create_app(repository)),
            base_url="http://payment.test",
        ) as client:

            async def post_terminal(path: str, key: str) -> None:
                nonlocal ready_count
                async with ready_lock:
                    ready_count += 1
                    if ready_count == 2:
                        ready.set()
                await start.wait()
                payload = {
                    "orderId": order_created.orderId,
                    "amount": order_created.amount,
                }
                responses.append(
                    await client.post(
                        path,
                        headers=_headers(order_created.userId, key),
                        json=payload,
                    ),
                )

            # When
            async with anyio.create_task_group() as task_group:
                task_group.start_soon(
                    post_terminal,
                    "/payments/mock-approvals",
                    "approval-race",
                )
                task_group.start_soon(
                    post_terminal,
                    "/payments/mock-failures",
                    "failure-race",
                )
                await ready.wait()
                start.set()

        # Then
        assert sorted(response.status_code for response in responses) == [201, 409]
        async with session_factory() as session:
            persisted = (
                await session.execute(
                    text(
                        "SELECT (SELECT count(*) FROM payments), "
                        "(SELECT count(*) FROM outbox_events), "
                        "(SELECT status FROM payments), "
                        "(SELECT event_type FROM outbox_events), "
                        "(SELECT topic FROM outbox_events), "
                        "(SELECT message_key FROM outbox_events)",
                    ),
                )
            ).one()
        assert persisted[0:2] == (1, 1)
        assert persisted[3] == f"payment.{persisted[2].lower()}"
        assert persisted[4] == persisted[3]
        assert persisted[5] == order_created.orderId


@pytest.mark.anyio
async def test_duplicate_approval_reuses_payment_and_outbox() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresPaymentRepository(session_factory)
        event = _order_created("approval-replay")
        await repository.record_order_created(event)
        app = create_app(repository)
        headers = _headers(event.userId, "approval-replay")
        payload = {"orderId": event.orderId, "amount": event.amount}

        # When
        async with AsyncClient(
            transport=ASGITransport(app=app),
            base_url="http://payment.test",
        ) as client:
            first = await client.post(
                "/payments/mock-approvals", headers=headers, json=payload
            )
            replay = await client.post(
                "/payments/mock-approvals", headers=headers, json=payload
            )

        # Then
        assert (first.status_code, replay.status_code) == (201, 201)
        assert first.json()["data"]["id"] == replay.json()["data"]["id"]
        assert await _payment_and_outbox_counts(session_factory) == (1, 1)


@pytest.mark.anyio
async def test_duplicate_failure_reuses_payment_and_outbox() -> None:
    # Given
    database_url = os.environ[PAYMENT_TEST_DATABASE_URL]
    async with postgres_schema(database_url) as engine:
        session_factory = async_sessionmaker(engine, expire_on_commit=False)
        repository = PostgresPaymentRepository(session_factory)
        event = _order_created("failure-replay")
        await repository.record_order_created(event)
        app = create_app(repository)
        headers = _headers(event.userId, "failure-replay")
        payload = {
            "orderId": event.orderId,
            "amount": event.amount,
            "reason": "card_declined",
        }

        # When
        async with AsyncClient(
            transport=ASGITransport(app=app),
            base_url="http://payment.test",
        ) as client:
            first = await client.post(
                "/payments/mock-failures", headers=headers, json=payload
            )
            replay = await client.post(
                "/payments/mock-failures", headers=headers, json=payload
            )

        # Then
        assert (first.status_code, replay.status_code) == (201, 201)
        assert first.json()["data"]["id"] == replay.json()["data"]["id"]
        assert await _payment_and_outbox_counts(session_factory) == (1, 1)


def _order_created(suffix: str) -> OrderCreatedEvent:
    return OrderCreatedEvent(
        eventId=f"evt-{suffix}",
        userId="00000000-0000-4000-8000-000000000001",
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


def _headers(user_id: str, idempotency_key: str) -> dict[str, str]:
    return {
        "X-User-Id": user_id,
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": idempotency_key,
    }


async def _payment_and_outbox_counts(
    session_factory: async_sessionmaker[AsyncSession],
) -> tuple[int, int]:
    async with session_factory() as session:
        row = (
            await session.execute(
                text(
                    "SELECT (SELECT count(*) FROM payments), "
                    "(SELECT count(*) FROM outbox_events)",
                ),
            )
        ).one()
    return int(row[0]), int(row[1])
