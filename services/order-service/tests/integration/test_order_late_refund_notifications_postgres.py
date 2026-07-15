import pytest
from contracts import RefundCompletedEvent, RefundFailedEvent
from sqlalchemy import text

from app.models import OrderId
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    OCCURRED_AT,
    approved,
    command,
    inventory_repository,
    product,
)


@pytest.mark.anyio
async def test_late_approval_refund_results_emit_each_typed_notification_once() -> None:
    # Given
    product_for_sale = product("late-refund-results")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        completed_order = await repository.create_order(
            command(product_for_sale, "late-completed")
        )
        failed_order = await repository.create_order(
            command(product_for_sale, "late-failed")
        )
        assert isinstance(completed_order, OrderCreated)
        assert isinstance(failed_order, OrderCreated)
        for created in (completed_order, failed_order):
            await repository.expire_pending_order(
                OrderId(created.order.id), OCCURRED_AT
            )
            await repository.apply_payment_approved(
                approved(created.order.id, created.order.userId, created.order.amount)
            )
        async with session_factory() as session:
            refund_requests = {
                str(order_id): (str(refund_id), str(payment_id))
                for order_id, refund_id, payment_id in (
                    await session.execute(
                        text(
                            "SELECT aggregate_id, payload->>'refundId', "
                            "payload->>'paymentId' FROM outbox_events "
                            "WHERE event_type='refund.requested'"
                        )
                    )
                ).all()
            }
        completed_refund_id, completed_payment_id = refund_requests[
            completed_order.order.id
        ]
        failed_refund_id, failed_payment_id = refund_requests[failed_order.order.id]
        completed_event = RefundCompletedEvent(
            eventId="evt-late-refund-completed",
            userId=completed_order.order.userId,
            sourceId=completed_refund_id,
            occurredAt=OCCURRED_AT,
            producer="payment-service",
            refundId=completed_refund_id,
            orderId=completed_order.order.id,
            paymentId=completed_payment_id,
            amount=completed_order.order.amount,
        )
        failed_event = RefundFailedEvent(
            eventId="evt-late-refund-failed",
            userId=failed_order.order.userId,
            sourceId=failed_refund_id,
            occurredAt=OCCURRED_AT,
            producer="payment-service",
            refundId=failed_refund_id,
            orderId=failed_order.order.id,
            paymentId=failed_payment_id,
            amount=failed_order.order.amount,
            reason="provider retry exhausted",
        )

        # When
        completed_results = [
            await repository.apply_refund_completed(completed_event) for _ in range(3)
        ]
        failed_results = [
            await repository.apply_refund_failed(failed_event) for _ in range(3)
        ]

        # Then
        async with session_factory() as session:
            notifications = (
                await session.execute(
                    text(
                        "SELECT aggregate_id, payload->>'notificationId', "
                        "payload->>'notificationType' FROM outbox_events "
                        "WHERE event_type='notification.requested'"
                    )
                )
            ).all()
        assert completed_results == [True, False, False]
        assert failed_results == [True, False, False]
        assert set(notifications) == {
            (
                completed_order.order.id,
                f"notification-order-expired-{completed_order.order.id}",
                "ORDER_EXPIRED",
            ),
            (
                completed_order.order.id,
                f"notification-payment-refunded-{completed_order.order.id}",
                "PAYMENT_REFUNDED",
            ),
            (
                failed_order.order.id,
                f"notification-order-expired-{failed_order.order.id}",
                "ORDER_EXPIRED",
            ),
            (
                failed_order.order.id,
                f"notification-refund_failed-{failed_order.order.id}",
                "REFUND_FAILED",
            ),
        }
