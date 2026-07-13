from datetime import UTC, datetime

from app.events import notification_requested_event, order_created_event
from app.models import IdempotencyKey, Order, OrderStatus


def test_order_created_event_uses_order_id_as_purchase_correlation() -> None:
    order = purchase_order()

    event = order_created_event(order, IdempotencyKey("create-order-001"))

    assert event.correlationId == order.id


def test_notification_requested_event_uses_order_id_as_purchase_correlation() -> None:
    order = purchase_order()

    event = notification_requested_event(order)

    assert event.correlationId == order.id


def purchase_order() -> Order:
    return Order(
        id="order-001",
        userId="user-001",
        dropId="drop-001",
        productId="product-001",
        quantity=1,
        amount=50000,
        status=OrderStatus.CONFIRMED,
        createdAt=datetime(2026, 7, 13, tzinfo=UTC),
    )
