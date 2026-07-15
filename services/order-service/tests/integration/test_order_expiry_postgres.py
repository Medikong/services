from datetime import timedelta

import pytest
from sqlalchemy import text

from app.models import OrderId
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    OCCURRED_AT,
    approved,
    command,
    failed,
    inbox_count,
    inventory_repository,
    inventory_state,
    order_status,
    product,
    refund,
)


@pytest.mark.anyio
async def test_expiry_emits_inventory_order_and_typed_notification_once() -> None:
    # Given
    product_for_sale = product("expiry-events")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "events"))
        assert isinstance(created, OrderCreated)
        async with session_factory.begin() as session:
            await session.execute(
                text("UPDATE orders SET expires_at=:now WHERE id=:order_id"),
                {"now": OCCURRED_AT, "order_id": created.order.id},
            )

        # When
        first = await repository.expire_due_order(OCCURRED_AT)
        duplicate = await repository.expire_pending_order(
            OrderId(created.order.id), OCCURRED_AT
        )

        # Then
        async with session_factory() as session:
            events = (
                await session.execute(
                    text(
                        "SELECT event_type, payload->>'notificationType' "
                        "FROM outbox_events WHERE aggregate_id=:order_id "
                        "AND event_type IN ('order.expired','notification.requested') "
                        "ORDER BY event_type"
                    ),
                    {"order_id": created.order.id},
                )
            ).all()
        assert first is True
        assert duplicate is False
        assert events == [
            ("notification.requested", "ORDER_EXPIRED"),
            ("order.expired", None),
        ]
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)


@pytest.mark.anyio
async def test_failure_before_deadline_prevents_expiry() -> None:
    # Given
    product_for_sale = product("failure-before-expiry")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "failure"))
        assert isinstance(created, OrderCreated)

        # When
        await repository.apply_payment_failed(
            failed(
                created.order.id,
                created.order.userId,
                created.order.amount,
                "pre-expiry",
            )
        )
        expired = await repository.expire_due_order(OCCURRED_AT + timedelta(days=365))

        # Then
        assert expired is False
        assert await order_status(session_factory, created.order.id) == "PAYMENT_FAILED"
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)


@pytest.mark.anyio
async def test_late_approval_requests_exactly_one_full_refund() -> None:
    # Given
    product_for_sale = product("late-approval")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "late"))
        assert isinstance(created, OrderCreated)
        await repository.expire_pending_order(OrderId(created.order.id), OCCURRED_AT)
        first_approval = approved(
            created.order.id,
            created.order.userId,
            created.order.amount,
        )
        invalid_approval = first_approval.model_copy(update={"userId": "attacker"})
        replayed_delivery = first_approval.model_copy(
            update={"eventId": f"{first_approval.eventId}-redelivery"}
        )

        # When
        await repository.apply_payment_approved(invalid_approval)
        await repository.apply_payment_approved(first_approval)
        await repository.apply_payment_approved(first_approval)
        await repository.apply_payment_approved(replayed_delivery)
        await repository.apply_payment_failed(
            failed(
                created.order.id,
                created.order.userId,
                created.order.amount,
                "late",
            )
        )
        await repository.apply_refund_completed(
            refund(created.order.id, "terminal", "evt-refund-completed-late")
        )
        await repository.apply_refund_completed(
            refund(created.order.id, "terminal", "evt-refund-completed-late")
        )

        # Then
        async with session_factory() as session:
            refund_rows = (
                await session.execute(
                    text(
                        "SELECT payload->>'orderId', payload->>'paymentId', "
                        "(payload->>'amount')::int, payload->>'reason' "
                        "FROM outbox_events WHERE event_type='refund.requested'"
                    )
                )
            ).all()
        assert refund_rows == [
            (
                created.order.id,
                first_approval.paymentId,
                created.order.amount,
                "ORDER_EXPIRED_LATE_APPROVAL",
            )
        ]
        assert await order_status(session_factory, created.order.id) == "EXPIRED"
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)
        assert await inbox_count(session_factory) == 4


@pytest.mark.anyio
async def test_unknown_payment_approval_cannot_suppress_genuine_refund() -> None:
    # Given
    product_for_sale = product("unknown-payment")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "unknown"))
        assert isinstance(created, OrderCreated)
        await repository.expire_pending_order(OrderId(created.order.id), OCCURRED_AT)
        genuine = approved(
            created.order.id,
            created.order.userId,
            created.order.amount,
        )
        unknown = genuine.model_copy(
            update={
                "sourceId": "payment-unknown",
                "paymentId": "payment-unknown",
            }
        )

        # When
        await repository.apply_payment_approved(unknown)
        await repository.apply_payment_approved(genuine)

        # Then
        async with session_factory() as session:
            requested_payment_ids = list(
                (
                    await session.execute(
                        text(
                            "SELECT payload->>'paymentId' FROM outbox_events "
                            "WHERE event_type='refund.requested' ORDER BY event_id"
                        )
                    )
                ).scalars()
            )
        assert set(requested_payment_ids) == {genuine.paymentId, unknown.paymentId}
        assert await order_status(session_factory, created.order.id) == "EXPIRED"
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)
