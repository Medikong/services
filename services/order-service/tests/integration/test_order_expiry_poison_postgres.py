from datetime import datetime, timedelta

import anyio
import pytest
from sqlalchemy import select, text

from app.expiry import OrderExpiryWorker
from app.metrics import OrderMetrics
from app.postgres import PostgresOrderRepository
from app.records import OrderRecord
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    OCCURRED_AT,
    command,
    inventory_repository,
    inventory_state,
    order_status,
    product,
)


class OneShotExpirer:
    def __init__(self, repository: PostgresOrderRepository) -> None:
        self._repository = repository
        self._called = False

    async def expire_due_order(self, now: datetime) -> bool:
        if self._called:
            await anyio.sleep_forever()
        self._called = True
        return await self._repository.expire_due_order(now)


@pytest.mark.anyio
async def test_worker_restart_skips_oldest_due_order_without_inventory() -> None:
    # Given
    product_for_sale = product("poison-restart")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        first = await repository.create_order(command(product_for_sale, "valid-first"))
        second = await repository.create_order(
            command(product_for_sale, "valid-second")
        )
        assert isinstance(first, OrderCreated)
        assert isinstance(second, OrderCreated)
        poison_id = "order-poison-missing-inventory"
        async with session_factory.begin() as session:
            await session.execute(
                text(
                    "INSERT INTO orders (id, user_id, drop_id, product_id, quantity, "
                    "amount, status, idempotency_key, created_at, "
                    "fulfillment_status, expires_at) VALUES "
                    "(:id, 'user-poison', 'drop-missing', 'product-missing', 1, "
                    "50000, 'PENDING_PAYMENT', 'poison', :created_at, "
                    "'NOT_STARTED', :poison_expiry)"
                ),
                {
                    "id": poison_id,
                    "created_at": OCCURRED_AT - timedelta(minutes=1),
                    "poison_expiry": OCCURRED_AT - timedelta(seconds=3),
                },
            )
            await session.execute(
                text("UPDATE orders SET expires_at=:first_expiry WHERE id=:first_id"),
                {
                    "first_id": first.order.id,
                    "first_expiry": OCCURRED_AT - timedelta(seconds=2),
                },
            )
            await session.execute(
                text("UPDATE orders SET expires_at=:second_expiry WHERE id=:second_id"),
                {
                    "second_id": second.order.id,
                    "second_expiry": OCCURRED_AT - timedelta(seconds=1),
                },
            )
        blocker = session_factory()
        await blocker.begin()
        await blocker.execute(
            select(OrderRecord).where(OrderRecord.id == poison_id).with_for_update()
        )
        metrics = OrderMetrics("order-service", "test", "test")
        locked_repository = PostgresOrderRepository(
            session_factory,
            catalog=(product_for_sale,),
            metrics=metrics,
        )
        locked_worker = OrderExpiryWorker(locked_repository, lambda: OCCURRED_AT)
        metric_prefix = (
            'order_expiry_missing_inventory_due{service_name="order-service",'
            'service_version="test",service_environment="test"}'
        )
        assert f"{metric_prefix} 0\n" in metrics.render()

        # When
        assert await locked_worker.process_once() is True
        assert f"{metric_prefix} 1\n" in metrics.render()
        await blocker.commit()
        await blocker.close()
        second_poison_id = "order-second-poison-missing-inventory"
        async with session_factory.begin() as session:
            await session.execute(
                text(
                    "INSERT INTO orders (id, user_id, drop_id, product_id, quantity, "
                    "amount, status, idempotency_key, created_at, "
                    "fulfillment_status, expires_at) VALUES "
                    "(:id, 'user-poison-2', 'drop-missing-2', 'product-missing-2', 1, "
                    "50000, 'PENDING_PAYMENT', 'poison-2', :created_at, "
                    "'NOT_STARTED', :poison_expiry)"
                ),
                {
                    "id": second_poison_id,
                    "created_at": OCCURRED_AT - timedelta(minutes=1),
                    "poison_expiry": OCCURRED_AT - timedelta(seconds=3),
                },
            )
        fresh_repository = PostgresOrderRepository(
            session_factory,
            catalog=(product_for_sale,),
            metrics=metrics,
        )
        fresh_worker = OrderExpiryWorker(
            OneShotExpirer(fresh_repository),
            lambda: OCCURRED_AT,
        )
        async with anyio.create_task_group() as task_group:
            task_group.start_soon(fresh_worker.run)
            with anyio.fail_after(2):
                while await order_status(session_factory, second.order.id) != "EXPIRED":
                    await anyio.sleep(0.01)
            task_group.cancel_scope.cancel()

        # Then
        assert await order_status(session_factory, first.order.id) == "EXPIRED"
        assert await order_status(session_factory, second.order.id) == "EXPIRED"
        assert await order_status(session_factory, poison_id) == "PENDING_PAYMENT"
        assert (
            await order_status(session_factory, second_poison_id) == "PENDING_PAYMENT"
        )
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 4)
        assert f"{metric_prefix} 2\n" in metrics.render()
