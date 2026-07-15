from datetime import UTC, datetime

from contracts import RefundCompletedEvent, RefundFailedEvent, RefundStatus
from sqlalchemy import text

import pytest

from app.cancellations import (
    CancellationAlreadyRequested,
    CancellationRequested,
    RequestCancellationCommand,
)
from app.models import IdempotencyKey, Order
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    approved,
    command,
    inventory_repository,
    product,
)


@pytest.mark.anyio
async def test_refund_results_are_idempotent_and_restore_sold_inventory_once() -> None:
    # Given
    product_for_sale = product("refund-results")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "refund"))
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(
            approved(created.order.id, created.order.userId, created.order.amount)
        )
        requested = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=created.order.id,
                user_id=created.order.userId,
                idempotency_key=IdempotencyKey("cancel-refund-results"),
                reason="customer request",
            ),
        )
        assert isinstance(requested, CancellationRequested)
        failed = _refund_failed(
            "evt-refund-failed-order-001",
            requested.cancellation.id,
            created.order,
        )
        completed = _refund_completed(
            "evt-refund-completed-order-001",
            requested.cancellation.id,
            created.order,
        )

        # When
        first_failure = await repository.apply_refund_failed(failed)
        duplicate_failure = await repository.apply_refund_failed(failed)
        failure_replay = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=created.order.id,
                user_id=created.order.userId,
                idempotency_key=IdempotencyKey("cancel-refund-results"),
                reason="customer request",
            ),
        )
        completion = await repository.apply_refund_completed(completed)
        duplicate_completion = await repository.apply_refund_completed(completed)
        late_failure = await repository.apply_refund_failed(
            failed.model_copy(update={"eventId": "evt-refund-failed-order-late"})
        )

        # Then
        assert first_failure is True
        assert duplicate_failure is False
        assert isinstance(failure_replay, CancellationAlreadyRequested)
        assert failure_replay.cancellation.refundStatus is RefundStatus.REQUESTED
        assert completion is True
        assert duplicate_completion is False
        assert late_failure is False
        async with session_factory() as session:
            order_snapshot = (
                await session.execute(
                    text(
                        "SELECT o.status, o.canceled_at IS NOT NULL, "
                        "c.refund_status, i.reserved_quantity, "
                        "i.sold_quantity, i.version "
                        "FROM orders o "
                        "JOIN cancellation_requests c ON c.order_id=o.id "
                        "JOIN inventory_items i ON i.drop_id=o.drop_id "
                        "AND i.product_id=o.product_id WHERE o.id=:order_id"
                    ),
                    {"order_id": created.order.id},
                )
            ).one()
            refund_inbox = int(
                (
                    await session.execute(
                        text(
                            "SELECT count(*) FROM processed_events "
                            "WHERE event_type IN "
                            "('refund.completed', 'refund.failed')"
                        )
                    )
                ).scalar_one()
            )
            notification_types = list(
                (
                    await session.execute(
                        text(
                            "SELECT payload->>'notificationType' "
                            "FROM outbox_events "
                            "WHERE event_type='notification.requested' "
                            "AND aggregate_id=:order_id "
                            "ORDER BY occurred_at, event_id"
                        ),
                        {"order_id": created.order.id},
                    )
                ).scalars()
            )
        assert order_snapshot == ("CANCELED", True, "COMPLETED", 0, 0, 3)
        assert refund_inbox == 3
        assert sorted(notification_types) == [
            "ORDER_CANCELED",
            "ORDER_CONFIRMED",
            "REFUND_FAILED",
        ]


def _refund_failed(
    event_id: str,
    refund_id: str,
    order: Order,
) -> RefundFailedEvent:
    return RefundFailedEvent(
        eventId=event_id,
        userId=order.userId,
        sourceId=refund_id,
        occurredAt=datetime(2026, 7, 15, 2, 0, tzinfo=UTC),
        producer="payment-service",
        refundId=refund_id,
        orderId=order.id,
        paymentId=f"payment-{order.id}",
        amount=order.amount,
        reason="provider retry exhausted",
    )


@pytest.mark.anyio
async def test_invalid_refund_result_does_not_poison_genuine_event_id() -> None:
    # Given
    product_for_sale = product("refund-poison")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "poison"))
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(
            approved(created.order.id, created.order.userId, created.order.amount)
        )
        requested = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=created.order.id,
                user_id=created.order.userId,
                idempotency_key=IdempotencyKey("cancel-refund-poison"),
                reason="customer request",
            ),
        )
        assert isinstance(requested, CancellationRequested)
        genuine = _refund_completed(
            "evt-refund-completed-poison",
            requested.cancellation.id,
            created.order,
        )
        invalid = genuine.model_copy(update={"amount": genuine.amount + 1})
        oversized = genuine.model_copy(update={"eventId": "e" * 129})
        nul_byte = genuine.model_copy(update={"eventId": "evt-refund\x00poison"})

        # When
        oversized_result = await repository.apply_refund_completed(oversized)
        nul_byte_result = await repository.apply_refund_completed(nul_byte)
        invalid_result = await repository.apply_refund_completed(invalid)
        genuine_result = await repository.apply_refund_completed(genuine)

        # Then
        assert oversized_result is False
        assert nul_byte_result is False
        assert invalid_result is False
        assert genuine_result is True
        async with session_factory() as session:
            snapshot = (
                await session.execute(
                    text(
                        "SELECT o.status, c.refund_status, i.sold_quantity, "
                        "(SELECT count(*) FROM processed_events "
                        "WHERE event_id=:event_id) "
                        "FROM orders o "
                        "JOIN cancellation_requests c ON c.order_id=o.id "
                        "JOIN inventory_items i ON i.drop_id=o.drop_id "
                        "AND i.product_id=o.product_id WHERE o.id=:order_id"
                    ),
                    {
                        "event_id": genuine.eventId,
                        "order_id": created.order.id,
                    },
                )
            ).one()
        assert snapshot == ("CANCELED", "COMPLETED", 0, 1)


def _refund_completed(
    event_id: str,
    refund_id: str,
    order: Order,
) -> RefundCompletedEvent:
    return RefundCompletedEvent(
        eventId=event_id,
        userId=order.userId,
        sourceId=refund_id,
        occurredAt=datetime(2026, 7, 15, 2, 1, tzinfo=UTC),
        producer="payment-service",
        refundId=refund_id,
        orderId=order.id,
        paymentId=f"payment-{order.id}",
        amount=order.amount,
    )
