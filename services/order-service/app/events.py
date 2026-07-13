from datetime import UTC, datetime
from uuid import uuid4

from contracts import NotificationRequestedEvent, OrderCreatedEvent

from app.models import IdempotencyKey, Order

PRODUCER_NAME = "order-service"


def order_created_event(
    order: Order,
    idempotency_key: IdempotencyKey,
) -> OrderCreatedEvent:
    return OrderCreatedEvent(
        eventId=f"evt-{uuid4().hex}",
        userId=order.userId,
        sourceId=order.id,
        occurredAt=datetime.now(UTC),
        producer=PRODUCER_NAME,
        orderId=order.id,
        dropId=order.dropId,
        productId=order.productId,
        quantity=order.quantity,
        amount=order.amount,
        idempotencyKey=idempotency_key,
        correlationId=order.id,
    )


def notification_requested_event(order: Order) -> NotificationRequestedEvent:
    return NotificationRequestedEvent(
        eventId=f"evt-notification-requested-{order.id}",
        userId=order.userId,
        sourceId=order.id,
        occurredAt=datetime.now(UTC),
        producer=PRODUCER_NAME,
        notificationId=f"notification-{order.id}",
        orderId=order.id,
        title="주문이 확정되었습니다",
        message="DropMong 주문이 정상 처리되었습니다.",
        correlationId=order.id,
    )
