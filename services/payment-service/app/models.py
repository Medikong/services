from datetime import datetime
from enum import StrEnum, unique
from typing import NewType

from pydantic import BaseModel, ConfigDict, Field

IdempotencyKey = NewType("IdempotencyKey", str)
OrderId = NewType("OrderId", str)
PaymentId = NewType("PaymentId", str)
UserId = NewType("UserId", str)


@unique
class PaymentMethod(StrEnum):
    MOCK_CARD = "MOCK_CARD"


@unique
class PaymentStatus(StrEnum):
    APPROVED = "APPROVED"
    FAILED = "FAILED"


@unique
class UserRole(StrEnum):
    CUSTOMER = "CUSTOMER"
    OWNER = "OWNER"
    ADMIN = "ADMIN"


class ApprovePaymentRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    orderId: str = Field(min_length=1, max_length=64)
    amount: int = Field(ge=0)
    method: PaymentMethod = PaymentMethod.MOCK_CARD


class FailPaymentRequest(BaseModel):
    model_config = ConfigDict(frozen=True)

    orderId: str = Field(min_length=1, max_length=64)
    amount: int = Field(ge=0)
    method: PaymentMethod = PaymentMethod.MOCK_CARD
    reason: str | None = Field(default=None, min_length=1, max_length=128)


class Payment(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    orderId: str
    userId: str
    amount: int = Field(ge=0)
    method: PaymentMethod
    status: PaymentStatus
    createdAt: datetime
    approvedAt: datetime | None = None
    failedAt: datetime | None = None
    failureReason: str | None = None


class PaymentResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: Payment


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
