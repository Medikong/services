from datetime import datetime

from pydantic import BaseModel

from app.schemas.common import PageInfo


class CreateReservationRequest(BaseModel):
    performanceId: str
    seatId: str
    concertId: str | None = None
    showtimeId: str | None = None


class ReservationResponse(BaseModel):
    id: str
    userId: str
    performanceId: str
    seatId: str
    status: str
    expiresAt: datetime
    createdAt: datetime


class ReservationListResponse(BaseModel):
    items: list[ReservationResponse]
    page: PageInfo
