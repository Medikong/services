from pydantic import BaseModel
from typing import Any


class BusinessEvent(BaseModel):
    eventId: str
    eventType: str
    occurredAt: str
    producer: str
    correlationId: str | None = None

    # 공통 payload
    userId: int
    sourceId: str

    # 이벤트별 선택 필드
    concertId: str | None = None
    reservationId: str | None = None
    ticketId: str | None = None
    payload: dict[str, Any] | None = None
