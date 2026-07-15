from hashlib import blake2b

import pytest
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError

from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    approved,
    command,
    inbox_count,
    inventory_repository,
    inventory_state,
    order_status,
    product,
)


@pytest.mark.anyio
async def test_inventory_database_constraint_rejects_counter_overflow() -> None:
    # Given
    product_for_sale = product("constraint")
    async with inventory_repository(product_for_sale) as (_, session_factory):
        # When
        with pytest.raises(IntegrityError):
            async with session_factory.begin() as session:
                await session.execute(
                    text(
                        "UPDATE inventory_items SET reserved_quantity=43 "
                        "WHERE drop_id=:drop_id AND product_id=:product_id"
                    ),
                    {
                        "drop_id": product_for_sale.drop_id,
                        "product_id": product_for_sale.product_id,
                    },
                )

        # Then
        assert await inventory_state(session_factory, product_for_sale) == (42, 0, 0, 0)


@pytest.mark.anyio
async def test_approval_rolls_back_inbox_inventory_and_order_when_outbox_fails() -> (
    None
):
    # Given
    product_for_sale = product("atomic")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "atomic"))
        assert isinstance(created, OrderCreated)
        approval = approved(
            created.order.id, created.order.userId, created.order.amount
        )
        suffix = blake2b(
            f"approve:{approval.eventId}".encode(), digest_size=16
        ).hexdigest()
        conflicting_event_id = f"evt-inventory-{suffix}"
        async with session_factory.begin() as session:
            await session.execute(
                text(
                    "INSERT INTO outbox_events "
                    "(event_id,event_type,aggregate_type,aggregate_id,topic,message_key,"
                    "payload,occurred_at,attempts) VALUES "
                    "(:event_id,'inventory.changed','inventory','conflict',"
                    "'inventory.changed','conflict','{}'::jsonb,:occurred_at,0)"
                ),
                {
                    "event_id": conflicting_event_id,
                    "occurred_at": approval.occurredAt,
                },
            )

        # When
        with pytest.raises(IntegrityError):
            await repository.apply_payment_approved(approval)

        # Then
        assert (
            await order_status(session_factory, created.order.id) == "PENDING_PAYMENT"
        )
        assert await inventory_state(session_factory, product_for_sale) == (
            42,
            10,
            0,
            1,
        )
        assert await inbox_count(session_factory) == 0
