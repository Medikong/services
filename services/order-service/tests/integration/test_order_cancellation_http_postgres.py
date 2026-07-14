from httpx import ASGITransport, AsyncClient
from sqlalchemy import event, text

import pytest

from app.main import create_app
from app.models import UserId
from app.store import OrderCreated
from tests.integration.inventory_lifecycle_support import (
    approved,
    command,
    inventory_repository,
    product,
)


@pytest.mark.anyio
async def test_postgres_http_cancellation_returns_accepted_contract() -> None:
    # Given
    product_for_sale = product("http-cancel")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "http"))
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(approved(created.order.id))
        async with AsyncClient(
            transport=ASGITransport(app=create_app(repository)),
            base_url="http://order-service",
        ) as client:
            # When
            response = await client.post(
                f"/orders/{created.order.id}/cancellations",
                headers={
                    "X-User-Id": created.order.userId,
                    "X-User-Role": "CUSTOMER",
                    "Idempotency-Key": "cancel-http-postgres",
                },
                json={"reason": "customer request"},
            )
            status_response = await client.get(
                f"/orders/{created.order.id}/cancellations",
                headers={
                    "X-User-Id": created.order.userId,
                    "X-User-Role": "CUSTOMER",
                },
            )

        # Then
        assert response.status_code == 202
        assert response.json()["data"]["orderStatus"] == "CANCEL_PENDING"
        assert response.json()["data"]["refundStatus"] == "REQUESTED"
        assert status_response.status_code == 200
        assert status_response.json()["data"] == response.json()["data"]

        statements: list[str] = []

        def capture_select(
            _connection: object,
            _cursor: object,
            statement: str,
            _parameters: object,
            _context: object,
            _executemany: bool,
        ) -> None:
            if statement.lstrip().upper().startswith("SELECT"):
                statements.append(statement)

        engine = session_factory.kw["bind"].sync_engine
        event.listen(engine, "before_cursor_execute", capture_select)
        try:
            current = await repository.get_cancellation(
                created.order.id,
                UserId(created.order.userId),
            )
        finally:
            event.remove(engine, "before_cursor_execute", capture_select)

        assert current is not None
        assert len(statements) == 1
        assert "JOIN orders" in statements[0]


@pytest.mark.anyio
async def test_postgres_http_cancellation_rejects_shipped_order() -> None:
    # Given
    product_for_sale = product("http-shipped")
    async with inventory_repository(product_for_sale) as (repository, session_factory):
        created = await repository.create_order(command(product_for_sale, "shipped"))
        assert isinstance(created, OrderCreated)
        await repository.apply_payment_approved(approved(created.order.id))
        async with session_factory.begin() as session:
            await session.execute(
                text(
                    "UPDATE orders SET fulfillment_status='SHIPPED' WHERE id=:order_id"
                ),
                {"order_id": created.order.id},
            )
        async with AsyncClient(
            transport=ASGITransport(app=create_app(repository)),
            base_url="http://order-service",
        ) as client:
            # When
            response = await client.post(
                f"/orders/{created.order.id}/cancellations",
                headers={
                    "X-User-Id": created.order.userId,
                    "X-User-Role": "CUSTOMER",
                    "Idempotency-Key": "cancel-http-shipped",
                },
                json={"reason": "too late"},
            )

        # Then
        assert response.status_code == 409
        assert response.json()["error"] == {
            "code": "cancellation.not_allowed",
            "message": "order is not eligible for cancellation",
            "details": {"orderId": created.order.id},
        }
