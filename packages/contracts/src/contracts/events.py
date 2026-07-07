from datetime import datetime
from typing import Final, Literal

from pydantic import BaseModel, ConfigDict, Field


ORDER_CREATED_TOPIC: Final = "order.created"
PAYMENT_APPROVED_TOPIC: Final = "payment.approved"
PAYMENT_FAILED_TOPIC: Final = "payment.failed"
ORDER_CONFIRMED_TOPIC: Final = "order.confirmed"
NOTIFICATION_REQUESTED_TOPIC: Final = "notification.requested"


class BusinessEvent(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    eventId: str = Field(min_length=1, max_length=128)
    eventType: str
    userId: str
    sourceId: str
    occurredAt: datetime
    producer: str
    correlationId: str | None = None


class OrderCreatedEvent(BusinessEvent):
    eventType: Literal["order.created"] = ORDER_CREATED_TOPIC
    orderId: str
    dropId: str
    productId: str
    quantity: int = Field(ge=1)
    amount: int = Field(ge=0)
    idempotencyKey: str | None = None


class PaymentApprovedEvent(BusinessEvent):
    eventType: Literal["payment.approved"] = PAYMENT_APPROVED_TOPIC
    orderId: str
    paymentId: str
    amount: int = Field(ge=0)


class PaymentFailedEvent(BusinessEvent):
    eventType: Literal["payment.failed"] = PAYMENT_FAILED_TOPIC
    orderId: str
    paymentId: str
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


class NotificationRequestedEvent(BusinessEvent):
    eventType: Literal["notification.requested"] = NOTIFICATION_REQUESTED_TOPIC
    notificationId: str
    orderId: str
    channel: Literal["IN_APP"] = "IN_APP"
    title: str
    message: str
