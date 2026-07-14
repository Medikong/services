from sqlalchemy import text

import pytest

from app.cancellations import (
    CancellationAlreadyRequested,
    CancellationIdempotencyConflict,
    CancellationNotAllowed,
    CancellationRequested,
    RequestCancellationCommand,
)
from app.models import IdempotencyKey, OrderStatus
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    approved,
    command,
    inventory_repository,
    product,
)


@pytest.mark.anyio
async def test_confirmed_order_cancellation_commits_request_and_refund_once() -> None:
    # Given
    product_for_sale = product("cancellation")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(
            command(product_for_sale, "cancellation")
        )
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(approved(created.order.id))
        cancellation = RequestCancellationCommand(
            order_id=created.order.id,
            user_id=created.order.userId,
            idempotency_key=IdempotencyKey("cancel-postgres-001"),
            reason="customer request",
        )

        # When
        first = await repository.request_cancellation(cancellation)
        replayed = await repository.request_cancellation(cancellation)
        conflict = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=created.order.id,
                user_id=created.order.userId,
                idempotency_key=cancellation.idempotency_key,
                reason="changed reason",
            ),
        )

        # Then
        assert isinstance(first, CancellationRequested)
        assert isinstance(replayed, CancellationAlreadyRequested)
        assert isinstance(conflict, CancellationIdempotencyConflict)
        assert replayed.cancellation == first.cancellation
        async with session_factory() as session:
            snapshot = (
                await session.execute(
                    text(
                        """
                        SELECT o.status, o.cancel_pending_at IS NOT NULL,
                               c.reason, c.refund_status,
                               count(e.event_id) FILTER (
                                   WHERE e.event_type = 'refund.requested'
                               ),
                               max(e.payload->>'paymentId') FILTER (
                                   WHERE e.event_type = 'refund.requested'
                               )
                        FROM orders o
                        JOIN cancellation_requests c ON c.order_id = o.id
                        LEFT JOIN outbox_events e ON e.aggregate_id = o.id
                        WHERE o.id = :order_id
                        GROUP BY o.status, o.cancel_pending_at,
                                 c.reason, c.refund_status
                        """,
                    ),
                    {"order_id": created.order.id},
                )
            ).one()
        assert snapshot == (
            "CANCEL_PENDING",
            True,
            "customer request",
            "REQUESTED",
            1,
            f"payment-{created.order.id}",
        )


@pytest.mark.anyio
@pytest.mark.parametrize(
    ("order_status", "fulfillment_status"),
    [
        (OrderStatus.PENDING_PAYMENT, "NOT_STARTED"),
        (OrderStatus.PAYMENT_FAILED, "NOT_STARTED"),
        (OrderStatus.EXPIRED, "NOT_STARTED"),
        (OrderStatus.CANCELED, "NOT_STARTED"),
        (OrderStatus.CANCEL_PENDING, "NOT_STARTED"),
        (OrderStatus.CONFIRMED, "SHIPPED"),
    ],
)
async def test_ineligible_order_status_or_shipment_is_rejected(
    order_status: OrderStatus,
    fulfillment_status: str,
) -> None:
    # Given
    suffix = f"r-{order_status.value[:3].lower()}-{fulfillment_status[:1].lower()}"
    product_for_sale = product(suffix)
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, suffix))
        assert isinstance(created, OrderCreated)
        async with session_factory.begin() as session:
            await session.execute(
                text(
                    "UPDATE orders SET status=:status, "
                    "fulfillment_status=:fulfillment_status, "
                    "payment_id='payment-rejection' WHERE id=:order_id"
                ),
                {
                    "status": order_status.value,
                    "fulfillment_status": fulfillment_status,
                    "order_id": created.order.id,
                },
            )

        # When
        result = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=created.order.id,
                user_id=created.order.userId,
                idempotency_key=IdempotencyKey(f"cancel-{suffix}"),
                reason="not eligible",
            ),
        )

        # Then
        assert isinstance(result, CancellationNotAllowed)
        async with session_factory() as session:
            cancellation_count = int(
                (
                    await session.execute(
                        text("SELECT count(*) FROM cancellation_requests")
                    )
                ).scalar_one()
            )
        assert cancellation_count == 0
