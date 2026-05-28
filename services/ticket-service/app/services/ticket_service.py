from uuid import uuid4
from datetime import UTC, datetime

from fastapi import HTTPException, status
from sqlalchemy.orm import Session

from app import kafka, s3
from app.auth import UserContext
from app.config import settings
from app.models import ProcessedEvent, Ticket
from app.schemas import PaymentApprovedEvent, TicketIssueRequest


async def issue_ticket(db: Session, request: TicketIssueRequest) -> Ticket:
    # 중복 발행 방지
    existing = db.query(Ticket).filter(
        Ticket.reservation_id == request.reservationId
    ).first()
    if existing:
        return existing

    ticket = Ticket(
        reservation_id=request.reservationId,
        user_id=request.userId,
        concert_id=request.concertId,
        seat_id=request.seatId,
        status="ISSUED",
    )
    db.add(ticket)
    db.flush()

    # S3에 QR, PDF 업로드
    ticket.qr_url = s3.upload_qr(ticket.id, request.reservationId)
    ticket.pdf_url = s3.upload_pdf(ticket.id, request.reservationId)

    db.commit()
    db.refresh(ticket)

    # ticket-issued 이벤트 발행
    await kafka.publish_event(settings.ticket_issued_topic, _ticket_issued_event(ticket))

    return ticket


def get_ticket(db: Session, ticket_id: int, user: UserContext) -> Ticket:
    ticket = db.get(Ticket, ticket_id)
    if ticket is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Ticket not found")
    if ticket.user_id != user.user_id:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Not allowed")
    return ticket


def list_my_tickets(db: Session, user: UserContext) -> list[Ticket]:
    return db.query(Ticket).filter(
        Ticket.user_id == user.user_id
    ).order_by(Ticket.id).all()


async def handle_payment_approved(db: Session, payload: dict) -> None:
    event = PaymentApprovedEvent.model_validate(payload)

    # idempotency: 이미 처리된 이벤트 중복 처리 방지
    processed = db.query(ProcessedEvent).filter(
        ProcessedEvent.event_id == event.eventId
    ).first()
    if processed:
        return

    request = TicketIssueRequest(
        reservationId=event.reservationId,
        userId=event.userId,
        concertId=event.concertId,
        seatId=event.seatId,
    )
    ticket = await issue_ticket(db, request)

    db.add(ProcessedEvent(event_id=event.eventId, ticket_id=ticket.id))
    db.commit()


def _ticket_issued_event(ticket: Ticket) -> dict:
    return {
        "eventId": str(uuid4()),
        "eventType": "ticket-issued",
        "userId": ticket.user_id,
        "sourceId": str(ticket.id),
        "reservationId": ticket.reservation_id,
        "occurredAt": datetime.now(UTC).isoformat(),
        "producer": "ticket-service",
    }
