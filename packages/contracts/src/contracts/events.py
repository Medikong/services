from datetime import datetime
from enum import StrEnum, unique
from typing import Final, Literal

from pydantic import BaseModel, ConfigDict, Field


ORDER_CREATED_TOPIC: Final = "order.created"
PAYMENT_APPROVED_TOPIC: Final = "payment.approved"
PAYMENT_FAILED_TOPIC: Final = "payment.failed"
ORDER_CONFIRMED_TOPIC: Final = "order.confirmed"
ORDER_EXPIRED_TOPIC: Final = "order.expired"
INVENTORY_CHANGED_TOPIC: Final = "inventory.changed"
REFUND_REQUESTED_TOPIC: Final = "refund.requested"
REFUND_COMPLETED_TOPIC: Final = "refund.completed"
REFUND_FAILED_TOPIC: Final = "refund.failed"
NOTIFICATION_REQUESTED_TOPIC: Final = "notification.requested"


@unique
class OrderStatus(StrEnum):
    PENDING_PAYMENT = "PENDING_PAYMENT"
    CONFIRMED = "CONFIRMED"
    PAYMENT_FAILED = "PAYMENT_FAILED"
    CANCEL_PENDING = "CANCEL_PENDING"
    CANCELED = "CANCELED"
    EXPIRED = "EXPIRED"


@unique
class OrderTransitionTrigger(StrEnum):
    PAYMENT_APPROVED = "PAYMENT_APPROVED"
    PAYMENT_FAILED = "PAYMENT_FAILED"
    PAYMENT_EXPIRED = "PAYMENT_EXPIRED"
    CANCELLATION_REQUESTED = "CANCELLATION_REQUESTED"
    REFUND_COMPLETED = "REFUND_COMPLETED"
    REFUND_FAILED = "REFUND_FAILED"


@unique
class RefundStatus(StrEnum):
    REQUESTED = "REQUESTED"
    PROCESSING = "PROCESSING"
    COMPLETED = "COMPLETED"
    FAILED = "FAILED"


@unique
class FulfillmentStatus(StrEnum):
    NOT_STARTED = "NOT_STARTED"
    PREPARING = "PREPARING"
    SHIPPED = "SHIPPED"


@unique
class NotificationType(StrEnum):
    ORDER_CONFIRMED = "ORDER_CONFIRMED"
    PAYMENT_FAILED = "PAYMENT_FAILED"
    ORDER_EXPIRED = "ORDER_EXPIRED"
    ORDER_CANCELED = "ORDER_CANCELED"
    PAYMENT_REFUNDED = "PAYMENT_REFUNDED"
    REFUND_FAILED = "REFUND_FAILED"


OrderTransition = tuple[OrderStatus, OrderStatus, OrderTransitionTrigger]

ALLOWED_ORDER_TRANSITIONS: Final[frozenset[OrderTransition]] = frozenset(
    {
        (
            OrderStatus.PENDING_PAYMENT,
            OrderStatus.CONFIRMED,
            OrderTransitionTrigger.PAYMENT_APPROVED,
        ),
        (
            OrderStatus.PENDING_PAYMENT,
            OrderStatus.PAYMENT_FAILED,
            OrderTransitionTrigger.PAYMENT_FAILED,
        ),
        (
            OrderStatus.PENDING_PAYMENT,
            OrderStatus.EXPIRED,
            OrderTransitionTrigger.PAYMENT_EXPIRED,
        ),
        (
            OrderStatus.CONFIRMED,
            OrderStatus.CANCEL_PENDING,
            OrderTransitionTrigger.CANCELLATION_REQUESTED,
        ),
        (
            OrderStatus.CANCEL_PENDING,
            OrderStatus.CANCELED,
            OrderTransitionTrigger.REFUND_COMPLETED,
        ),
    }
)


def is_order_transition_allowed(
    current: OrderStatus,
    target: OrderStatus,
    trigger: OrderTransitionTrigger,
) -> bool:
    """Return whether the purchase lifecycle declares this exact transition."""
    return (current, target, trigger) in ALLOWED_ORDER_TRANSITIONS


class BusinessEvent(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    schemaVersion: int = Field(default=1, ge=1)
    eventId: str = Field(min_length=1, max_length=128)
    eventType: str
    userId: str
    sourceId: str
    occurredAt: datetime
    producer: str
    correlationId: str | None = None


class OrderCreatedEvent(BusinessEvent):
    eventType: Literal["order.created"] = ORDER_CREATED_TOPIC
    orderId: str = Field(min_length=1, max_length=64)
    dropId: str
    productId: str
    quantity: int = Field(ge=1)
    amount: int = Field(ge=0)
    idempotencyKey: str | None = None


class PaymentApprovedEvent(BusinessEvent):
    eventType: Literal["payment.approved"] = PAYMENT_APPROVED_TOPIC
    orderId: str = Field(min_length=1, max_length=64)
    paymentId: str = Field(min_length=1, max_length=64)
    amount: int = Field(ge=0)


class PaymentFailedEvent(BusinessEvent):
    eventType: Literal["payment.failed"] = PAYMENT_FAILED_TOPIC
    orderId: str = Field(min_length=1, max_length=64)
    paymentId: str = Field(min_length=1, max_length=64)
    amount: int = Field(ge=0)
    reason: str | None = None


class OrderConfirmedEvent(BusinessEvent):
    eventType: Literal["order.confirmed"] = ORDER_CONFIRMED_TOPIC
    orderId: str
    paymentId: str
    dropId: str
    productId: str
    quantity: int = Field(ge=1)
    amount: int = Field(ge=0)


class OrderExpiredEvent(BusinessEvent):
    eventType: Literal["order.expired"] = ORDER_EXPIRED_TOPIC
    orderId: str
    dropId: str
    productId: str
    quantity: int = Field(ge=1)
    amount: int = Field(ge=0)


class InventoryChangedEvent(BusinessEvent):
    eventType: Literal["inventory.changed"] = INVENTORY_CHANGED_TOPIC
    dropId: str
    productId: str
    totalQuantity: int = Field(ge=0)
    reservedQuantity: int = Field(ge=0)
    soldQuantity: int = Field(ge=0)
    remainingQuantity: int = Field(ge=0)
    inventoryVersion: int = Field(ge=1)


class RefundRequestedEvent(BusinessEvent):
    eventType: Literal["refund.requested"] = REFUND_REQUESTED_TOPIC
    refundId: str
    orderId: str
    paymentId: str
    amount: int = Field(ge=0)
    reason: str = Field(min_length=1, max_length=500)


class RefundCompletedEvent(BusinessEvent):
    eventType: Literal["refund.completed"] = REFUND_COMPLETED_TOPIC
    refundId: str
    orderId: str
    paymentId: str
    amount: int = Field(ge=0)


class RefundFailedEvent(BusinessEvent):
    eventType: Literal["refund.failed"] = REFUND_FAILED_TOPIC
    refundId: str
    orderId: str
    paymentId: str
    amount: int = Field(ge=0)
    reason: str = Field(min_length=1, max_length=500)


class NotificationRequestedEvent(BusinessEvent):
    eventType: Literal["notification.requested"] = NOTIFICATION_REQUESTED_TOPIC
    notificationId: str
    orderId: str = Field(min_length=1, max_length=64)
    notificationType: NotificationType = NotificationType.ORDER_CONFIRMED
    channel: Literal["IN_APP"] = "IN_APP"
    title: str
    message: str
