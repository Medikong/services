import anyio
import pytest
from sqlalchemy import select, text
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from app.records import InventoryItemRecord
from app.store import OrderCreated, PaymentApplied, PaymentIgnored
from tests.integration.inventory_lifecycle_support import (
    OCCURRED_AT,
    approved,
    command,
    inventory_repository,
    inventory_state,
    order_status,
    product,
)


async def _wait_for_blocked_query(
    session_factory: async_sessionmaker[AsyncSession],
    relation_name: str,
) -> None:
    with anyio.fail_after(5):
        while True:
            async with session_factory() as session:
                blocked = int(
                    (
                        await session.execute(
                            text(
                                "SELECT count(*) FROM pg_stat_activity "
                                "WHERE datname=current_database() "
                                "AND pid<>pg_backend_pid() "
                                "AND wait_event_type='Lock' AND query ILIKE :query"
                            ),
                            {"query": f"%{relation_name}%"},
                        )
                    ).scalar_one()
                )
            if blocked > 0:
                return
            await anyio.sleep(0.01)


async def _lock_inventory(
    session: AsyncSession,
    drop_id: str,
    product_id: str,
) -> None:
    await session.execute(
        select(InventoryItemRecord)
        .where(
            InventoryItemRecord.drop_id == drop_id,
            InventoryItemRecord.product_id == product_id,
        )
        .with_for_update()
    )


@pytest.mark.anyio
async def test_approval_holding_order_lock_wins_over_expiry() -> None:
    # Given
    product_for_sale = product("approval-race")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "approval"))
        assert isinstance(created, OrderCreated)
        async with session_factory.begin() as session:
            await session.execute(
                text("UPDATE orders SET expires_at=:now WHERE id=:order_id"),
                {"now": OCCURRED_AT, "order_id": created.order.id},
            )
        approval_results: list[PaymentApplied | PaymentIgnored] = []
        expiry_processed = True
        blocker = session_factory()
        await blocker.begin()
        await _lock_inventory(
            blocker, product_for_sale.drop_id, product_for_sale.product_id
        )

        async def apply_approval() -> None:
            result = await repository.apply_payment_approved(
                approved(created.order.id, created.order.userId, created.order.amount)
            )
            assert isinstance(result, (PaymentApplied, PaymentIgnored))
            approval_results.append(result)

        # When
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(apply_approval)
            await _wait_for_blocked_query(session_factory, "inventory_items")
            expiry_processed = await repository.expire_due_order(OCCURRED_AT)
            await blocker.commit()
        await blocker.close()

        # Then
        assert expiry_processed is False
        assert len(approval_results) == 1
        assert isinstance(approval_results[0], PaymentApplied)
        assert await order_status(session_factory, created.order.id) == "CONFIRMED"
        assert await inventory_state(session_factory, product_for_sale) == (
            42,
            0,
            10,
            2,
        )


@pytest.mark.anyio
async def test_expiry_holding_order_lock_wins_and_late_approval_refunds() -> None:
    # Given
    product_for_sale = product("expiry-race")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "expiry"))
        assert isinstance(created, OrderCreated)
        async with session_factory.begin() as session:
            await session.execute(
                text("UPDATE orders SET expires_at=:now WHERE id=:order_id"),
                {"now": OCCURRED_AT, "order_id": created.order.id},
            )
        expiry_results: list[bool] = []
        approval_results: list[PaymentApplied | PaymentIgnored] = []
        blocker = session_factory()
        await blocker.begin()
        await _lock_inventory(
            blocker, product_for_sale.drop_id, product_for_sale.product_id
        )

        async def apply_expiry() -> None:
            expiry_results.append(await repository.expire_due_order(OCCURRED_AT))

        async def apply_approval() -> None:
            result = await repository.apply_payment_approved(
                approved(created.order.id, created.order.userId, created.order.amount)
            )
            assert isinstance(result, (PaymentApplied, PaymentIgnored))
            approval_results.append(result)

        # When
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(apply_expiry)
            await _wait_for_blocked_query(session_factory, "inventory_items")
            task_group.start_soon(apply_approval)
            await _wait_for_blocked_query(session_factory, "orders")
            await blocker.commit()
        await blocker.close()

        # Then
        async with session_factory() as session:
            refund_count = int(
                (
                    await session.execute(
                        text(
                            "SELECT count(*) FROM outbox_events "
                            "WHERE event_type='refund.requested'"
                        )
                    )
                ).scalar_one()
            )
        assert expiry_results == [True]
        assert len(approval_results) == 1
        assert isinstance(approval_results[0], PaymentIgnored)
        assert refund_count == 1
        assert await order_status(session_factory, created.order.id) == "EXPIRED"
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 2)
