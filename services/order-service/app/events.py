from datetime import UTC, datetime
from hashlib import blake2b
from uuid import uuid4

from contracts import (
    InventoryChangedEvent,
    NotificationRequestedEvent,
    NotificationType,
    OrderCreatedEvent,
    OrderExpiredEvent,
    RefundRequestedEvent,
)

from app.models import IdempotencyKey, Order
from app.records import InventoryItemRecord

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


def order_expired_notification_event(
    order: Order,
    occurred_at: datetime,
) -> NotificationRequestedEvent:
    return NotificationRequestedEvent(
        eventId=f"evt-notification-order-expired-{order.id}",
        userId=order.userId,
        sourceId=order.id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        notificationId=f"notification-order-expired-{order.id}",
        orderId=order.id,
        notificationType=NotificationType.ORDER_EXPIRED,
        title="주문 결제 시간이 만료되었습니다",
        message="결제 시간이 지나 예약된 재고가 해제되었습니다.",
        correlationId=order.id,
    )


def inventory_changed_event(
    inventory: InventoryItemRecord,
    *,
    cause_id: str,
    occurred_at: datetime,
    user_id: str,
    order_id: str,
) -> InventoryChangedEvent:
    event_suffix = blake2b(cause_id.encode("utf-8"), digest_size=16).hexdigest()
    return InventoryChangedEvent(
        eventId=f"evt-inventory-{event_suffix}",
        userId=user_id,
        sourceId=order_id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        dropId=inventory.drop_id,
        productId=inventory.product_id,
        totalQuantity=inventory.total_quantity,
        reservedQuantity=inventory.reserved_quantity,
        soldQuantity=inventory.sold_quantity,
        remainingQuantity=(
            inventory.total_quantity
            - inventory.reserved_quantity
            - inventory.sold_quantity
        ),
        inventoryVersion=inventory.version,
        correlationId=order_id,
    )


def inventory_snapshot_event(
    inventory: InventoryItemRecord,
    event_id: str,
    occurred_at: datetime,
) -> InventoryChangedEvent:
    """Build a fresh snapshot event without changing authoritative inventory."""
    aggregate_id = f"{inventory.drop_id}:{inventory.product_id}"
    return InventoryChangedEvent(
        eventId=event_id,
        userId="system",
        sourceId=aggregate_id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        dropId=inventory.drop_id,
        productId=inventory.product_id,
        totalQuantity=inventory.total_quantity,
        reservedQuantity=inventory.reserved_quantity,
        soldQuantity=inventory.sold_quantity,
        remainingQuantity=(
            inventory.total_quantity
            - inventory.reserved_quantity
            - inventory.sold_quantity
        ),
        inventoryVersion=inventory.version,
        correlationId=event_id,
    )


def order_expired_event(order: Order, occurred_at: datetime) -> OrderExpiredEvent:
    return OrderExpiredEvent(
        eventId=f"evt-order-expired-{order.id}",
        userId=order.userId,
        sourceId=order.id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        orderId=order.id,
        dropId=order.dropId,
        productId=order.productId,
        quantity=order.quantity,
        amount=order.amount,
        correlationId=order.id,
    )


def late_approval_refund_requested_event(
    order: Order,
    payment_id: str,
    occurred_at: datetime,
) -> RefundRequestedEvent:
    refund_suffix = blake2b(
        f"expired:{order.id}:{payment_id}".encode("utf-8"),
        digest_size=16,
    ).hexdigest()
    refund_id = f"refund-{refund_suffix}"
    return RefundRequestedEvent(
        eventId=f"evt-refund-requested-{refund_id}",
        userId=order.userId,
        sourceId=order.id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        correlationId=order.id,
        refundId=refund_id,
        orderId=order.id,
        paymentId=payment_id,
        amount=order.amount,
        reason="ORDER_EXPIRED_LATE_APPROVAL",
    )
