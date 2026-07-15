import pytest

from app.store import (
    OrderCreated,
    PaymentApplied,
    PaymentEventOrderMissing,
    PaymentFailureApplied,
    PaymentIgnored,
)
from tests.integration.inventory_lifecycle_support import (
    approved,
    command,
    failed,
    inbox_count,
    inventory_repository,
    inventory_state,
    order_status,
    product,
)


@pytest.mark.anyio
async def test_forged_approval_event_id_cannot_block_genuine_pre_expiry() -> None:
    # Given
    product_for_sale = product("approval-security")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(
            command(product_for_sale, "approval-sec")
        )
        assert isinstance(created, OrderCreated)
        genuine = approved(
            created.order.id,
            created.order.userId,
            created.order.amount,
        )
        forged = genuine.model_copy(update={"producer": "forged-service"})

        # When
        forged_result = await repository.apply_payment_approved(forged)
        genuine_result = await repository.apply_payment_approved(genuine)

        # Then
        assert isinstance(forged_result, PaymentIgnored)
        assert isinstance(genuine_result, PaymentApplied)
        assert await order_status(session_factory, created.order.id) == "CONFIRMED"
        assert await inbox_count(session_factory) == 1
        assert await inventory_state(session_factory, product_for_sale) == (
            42,
            0,
            10,
            2,
        )


@pytest.mark.anyio
async def test_forged_failure_event_id_cannot_block_genuine_pre_expiry() -> None:
    # Given
    product_for_sale = product("failure-security")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(
            command(product_for_sale, "failure-sec")
        )
        assert isinstance(created, OrderCreated)
        genuine = failed(
            created.order.id,
            created.order.userId,
            created.order.amount,
            "security",
        )
        forged = genuine.model_copy(update={"amount": genuine.amount + 1})

        # When
        forged_result = await repository.apply_payment_failed(forged)
        genuine_result = await repository.apply_payment_failed(genuine)

        # Then
        assert isinstance(forged_result, PaymentIgnored)
        assert isinstance(genuine_result, PaymentFailureApplied)
        assert await order_status(session_factory, created.order.id) == "PAYMENT_FAILED"
        assert await inbox_count(session_factory) == 1
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)


@pytest.mark.anyio
async def test_forged_missing_order_approval_cannot_poison_genuine_event_id(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    product_for_sale = product("missing-approval-security")
    future_order_id = "order-future-approval-security"
    order_command = command(product_for_sale, "missing-approval-sec")
    genuine = approved(future_order_id, order_command.user_id, 500000)
    forged = genuine.model_copy(update={"producer": "forged-service"})
    monkeypatch.setattr("app.postgres._new_order_id", lambda: future_order_id)

    async with inventory_repository(product_for_sale) as (repository, session_factory):
        # When
        forged_result = await repository.apply_payment_approved(forged)
        inbox_after_forged = await inbox_count(session_factory)
        created = await repository.create_order(order_command)
        genuine_result = await repository.apply_payment_approved(genuine)

        # Then
        assert isinstance(forged_result, PaymentEventOrderMissing)
        assert inbox_after_forged == 0
        assert isinstance(created, OrderCreated)
        assert isinstance(genuine_result, PaymentApplied)
        assert await inbox_count(session_factory) == 1


@pytest.mark.anyio
async def test_forged_missing_order_failure_cannot_poison_genuine_event_id(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    product_for_sale = product("missing-failure-security")
    future_order_id = "order-future-failure-security"
    order_command = command(product_for_sale, "missing-failure-sec")
    genuine = failed(
        future_order_id,
        order_command.user_id,
        500000,
        "missing-security",
    )
    forged = genuine.model_copy(update={"sourceId": "forged-payment"})
    monkeypatch.setattr("app.postgres._new_order_id", lambda: future_order_id)

    async with inventory_repository(product_for_sale) as (repository, session_factory):
        # When
        forged_result = await repository.apply_payment_failed(forged)
        inbox_after_forged = await inbox_count(session_factory)
        created = await repository.create_order(order_command)
        genuine_result = await repository.apply_payment_failed(genuine)

        # Then
        assert isinstance(forged_result, PaymentEventOrderMissing)
        assert inbox_after_forged == 0
        assert isinstance(created, OrderCreated)
        assert isinstance(genuine_result, PaymentFailureApplied)
        assert await inbox_count(session_factory) == 1
