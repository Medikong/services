from pydantic import BaseModel
from datetime import datetime


class TicketIssueRequest(BaseModel):
    reservationId: str
    userId: int
    concertId: str
    seatId: str


class TicketResponse(BaseModel):
    id: int
    reservationId: str
    userId: int
    concertId: str
    seatId: str
    status: str
    qrUrl: str | None
    pdfUrl: str | None
    issuedAt: datetime

    class Config:
        from_attributes = True


class PaymentApprovedEvent(BaseModel):
    eventId: str
    eventType: str
    userId: int
    sourceId: str          # payment_id
    reservationId: str
    concertId: str
    seatId: str
    occurredAt: str
    producer: str
    correlationId: str | None = None
