from datetime import datetime

from pydantic import BaseModel, ConfigDict


class TicketIssueRequest(BaseModel):
    reservationId: str
    userId: str
    concertId: str
    seatId: str


class TicketResponse(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    reservationId: str
    userId: str
    concertId: str
    seatId: str
    status: str
    qrUrl: str | None
    pdfUrl: str | None
    issuedAt: datetime
