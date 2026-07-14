import pytest
from app.models import OrderId
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    OCCURRED_AT,
    approved,
    command,
    failed,
    inbox_count,
    inventory_outbox,
    inventory_repository,
    inventory_state,
    mark_cancel_pending,
    order_status,
    outbox_types,
    product,
    refund,
)


@pytest.mark.anyio
async def test_payment_lifecycle_moves_inventory_once_and_emits_absolute_versions() -> (
    None
):
    # Given
    product_for_sale = product("payment")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "approval"))
        assert isinstance(created, OrderCreated)
        approval = approved(created.order.id)

        # When
        await repository.apply_payment_approved(approval)
        await repository.apply_payment_approved(approval)
        await repository.apply_payment_failed(failed(created.order.id, "late"))

        # Then
        assert await inventory_state(session_factory, product_for_sale) == (
            42,
            0,
            10,
            2,
        )
        assert await inventory_outbox(session_factory, product_for_sale) == [
            (42, 10, 0, 32, 1),
            (42, 0, 10, 32, 2),
        ]
        assert await inbox_count(session_factory) == 2


@pytest.mark.anyio
async def test_failure_and_expiry_release_reserved_inventory_once() -> None:
    # Given
    product_for_sale = product("release")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        failed_order = await repository.create_order(
            command(product_for_sale, "failure")
        )
        assert isinstance(failed_order, OrderCreated)
        failure = failed(failed_order.order.id, "declined")

        # When
        await repository.apply_payment_failed(failure)
        await repository.apply_payment_failed(failure)
        expired_order = await repository.create_order(
            command(product_for_sale, "expiry")
        )
        assert isinstance(expired_order, OrderCreated)
        first_expiry = await repository.expire_pending_order(
            OrderId(expired_order.order.id), OCCURRED_AT
        )
        second_expiry = await repository.expire_pending_order(
            OrderId(expired_order.order.id), OCCURRED_AT
        )

        # Then
        assert first_expiry is True
        assert second_expiry is False
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 4)
        assert sorted(await outbox_types(session_factory)) == sorted(
            [
                "inventory.changed",
                "order.created",
                "inventory.changed",
                "inventory.changed",
                "order.created",
                "inventory.changed",
                "order.expired",
            ]
        )


@pytest.mark.anyio
async def test_refund_completion_releases_sold_inventory_once() -> None:
    # Given
    product_for_sale = product("refund")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "refund"))
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(approved(created.order.id))
        await mark_cancel_pending(session_factory, created.order.id)
        completed_refund = refund(created.order.id, "001", "evt-refund-completed-001")

        # When
        first_result = await repository.apply_refund_completed(completed_refund)
        duplicate_result = await repository.apply_refund_completed(completed_refund)
        second_delivery = await repository.apply_refund_completed(
            refund(created.order.id, "001", "evt-refund-completed-002")
        )

        # Then
        assert first_result is True
        assert duplicate_result is False
        assert second_delivery is False
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 3)
        assert await order_status(session_factory, created.order.id) == "CANCELED"
        assert await inbox_count(session_factory) == 3
        assert (await inventory_outbox(session_factory, product_for_sale))[-1] == (
            42,
            0,
            0,
            42,
            3,
        )
