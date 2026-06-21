from datetime import datetime

from pydantic import BaseModel, ConfigDict


class TicketIssueRequest(BaseModel):
    reservationId: str
    userId: str
    concertId: str
    seatId: str


class TicketResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: str
    reservationId: str
    userId: str
    concertId: str
    seatId: str
    status: str
    qrUrl: str | None
    pdfUrl: str | None
    issuedAt: datetime


class TicketListResponse(BaseModel):
    items: list[TicketResponse]
    nextCursor: str | None = None
