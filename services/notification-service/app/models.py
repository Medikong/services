from datetime import datetime
from typing import NewType

from pydantic import BaseModel, ConfigDict, Field

from contracts import NotificationType

NotificationId = NewType("NotificationId", str)
OrderId = NewType("OrderId", str)
UserId = NewType("UserId", str)


class Notification(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str = Field(min_length=1, max_length=64)
    userId: str = Field(min_length=1, max_length=64)
    orderId: str | None = Field(default=None, max_length=64)
    type: NotificationType
    title: str = Field(min_length=1, max_length=120)
    message: str = Field(min_length=1, max_length=500)
    createdAt: datetime
    read: bool


class PageInfo(BaseModel):
    model_config = ConfigDict(frozen=True)

    nextCursor: str | None = None
    hasNext: bool


class NotificationListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[Notification, ...]
    pageInfo: PageInfo


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
