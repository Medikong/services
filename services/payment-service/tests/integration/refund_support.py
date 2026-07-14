from datetime import UTC, datetime
from typing import Final

from contracts import RefundRequestedEvent
from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncEngine

OCCURRED_AT: Final = datetime(2026, 7, 14, 12, 2, tzinfo=UTC)


def refund_requested_event() -> RefundRequestedEvent:
    return RefundRequestedEvent(
        eventId="evt-refund-requested",
        userId="user-refund",
        sourceId="order-refund",
        occurredAt=OCCURRED_AT,
        producer="order-service",
        correlationId="order-refund",
        refundId="refund-001",
        orderId="order-refund",
        paymentId="payment-refund",
        amount=50000,
        reason="customer_request",
    )


async def seed_payment_rows(engine: AsyncEngine) -> None:
    created_at = datetime(2026, 7, 14, 12, 0, tzinfo=UTC)
    async with engine.begin() as connection:
        await connection.execute(
            text(
                "INSERT INTO known_orders "
                "(order_id, user_id, amount, created_at) VALUES "
                "('order-refund', 'user-refund', 50000, :created_at), "
                "('order-other', 'user-refund', 50000, :created_at), "
                "('order-failed', 'user-failed', 50000, :created_at)",
            ),
            {"created_at": created_at},
        )
        await connection.execute(
            text(
                "INSERT INTO payments "
                "(id, order_id, user_id, amount, method, status, "
                "idempotency_key, created_at, approved_at, failed_at) VALUES "
                "('payment-refund', 'order-refund', 'user-refund', 50000, "
                "'MOCK_CARD', 'APPROVED', 'approved-key', :created_at, "
                ":created_at, NULL), "
                "('payment-other', 'order-other', 'user-refund', 50000, "
                "'MOCK_CARD', 'APPROVED', 'other-key', :created_at, "
                ":created_at, NULL), "
                "('payment-failed', 'order-failed', 'user-failed', 50000, "
                "'MOCK_CARD', 'FAILED', 'failed-key', :created_at, NULL, "
                ":created_at)",
            ),
            {"created_at": created_at},
        )
