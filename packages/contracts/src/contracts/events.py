from datetime import datetime
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


RESERVATION_CREATED_TOPIC = "reservation-created"
RESERVATION_EXPIRED_TOPIC = "reservation-expired"
PAYMENT_APPROVED_TOPIC = "payment-approved"
PAYMENT_FAILED_TOPIC = "payment-failed"
TICKET_ISSUED_TOPIC = "ticket-issued"


class BusinessEvent(BaseModel):
    model_config = ConfigDict(extra="forbid")

    eventId: str
    eventType: str
    userId: str
    sourceId: str
    occurredAt: datetime
    producer: str
    correlationId: str | None = None


class ReservationCreatedEvent(BusinessEvent):
    eventType: Literal["reservation-created"] = RESERVATION_CREATED_TOPIC
    reservationId: str
    concertId: str
    seatId: str
    performanceId: str | None = None
    amount: int | None = Field(default=None, ge=0)


class ReservationExpiredEvent(BusinessEvent):
    eventType: Literal["reservation-expired"] = RESERVATION_EXPIRED_TOPIC
    reservationId: str
    concertId: str
    seatId: str
    performanceId: str | None = None


class PaymentApprovedEvent(BusinessEvent):
    eventType: Literal["payment-approved"] = PAYMENT_APPROVED_TOPIC
    reservationId: str
    concertId: str
    seatId: str
    paymentId: str
    amount: int = Field(ge=0)
    performanceId: str | None = None


class PaymentFailedEvent(BusinessEvent):
    eventType: Literal["payment-failed"] = PAYMENT_FAILED_TOPIC
    reservationId: str
    concertId: str
    seatId: str
    paymentId: str
    amount: int = Field(ge=0)
    performanceId: str | None = None
    reason: str | None = None


class TicketIssuedEvent(BusinessEvent):
    eventType: Literal["ticket-issued"] = TICKET_ISSUED_TOPIC
    reservationId: str
    concertId: str
    seatId: str
    ticketId: str
    paymentId: str | None = None
    performanceId: str | None = None
