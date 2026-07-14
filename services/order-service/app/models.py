from datetime import datetime
from enum import StrEnum, unique
from typing import NewType

from pydantic import BaseModel, ConfigDict, Field

DropId = NewType("DropId", str)
IdempotencyKey = NewType("IdempotencyKey", str)
OrderId = NewType("OrderId", str)
PaymentId = NewType("PaymentId", str)
ProductId = NewType("ProductId", str)
UserId = NewType("UserId", str)


@unique
class OrderStatus(StrEnum):
    PENDING_PAYMENT = "PENDING_PAYMENT"
    CONFIRMED = "CONFIRMED"
    PAYMENT_FAILED = "PAYMENT_FAILED"
    CANCEL_PENDING = "CANCEL_PENDING"
    CANCELED = "CANCELED"
    EXPIRED = "EXPIRED"


@unique
class UserRole(StrEnum):
    CUSTOMER = "CUSTOMER"
    OWNER = "OWNER"
    ADMIN = "ADMIN"


class CreateOrderRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    productId: str
    quantity: int = Field(ge=1, le=10)


class Order(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    userId: str
    dropId: str
    productId: str
    quantity: int = Field(ge=1)
    amount: int = Field(ge=0)
    status: OrderStatus
    paymentId: str | None = None
    createdAt: datetime
    confirmedAt: datetime | None = None


class OrderResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: Order


class HealthResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    timestamp: datetime


class ReadinessResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    checks: dict[str, str]
    timestamp: datetime
