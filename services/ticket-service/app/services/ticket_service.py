from collections.abc import Callable
from uuid import uuid4
from datetime import UTC, datetime

from fastapi import HTTPException, status
from kafka_utils import build_producer_headers
from metrics import MetricResult
from sqlalchemy.orm import Session
from contracts.events import PaymentApprovedEvent, TicketIssuedEvent

from app import s3
from app.auth import UserContext
from app.config import settings
from app.kafka import KafkaProducer
from app.metrics.events import TicketEventPublishRecorded
from app.metrics.labels import TicketArtifact, TicketEventType, TicketSource
from app.metrics.recorder import TicketTelemetryRecorder
from app.models import ProcessedEvent, Ticket
from app.schemas import TicketIssueRequest


SessionFactory = Callable[[], Session]
ticket_metrics = TicketTelemetryRecorder()


class PaymentApprovedEventHandler:
    def __init__(self, db_session_factory: SessionFactory, kafka_producer: KafkaProducer) -> None:
        self._db_session_factory = db_session_factory
        self._kafka_producer = kafka_producer

    async def __call__(self, payload: dict) -> None:
        db = self._db_session_factory()
        try:
            await handle_payment_approved(db, payload, self._kafka_producer)
        finally:
            db.close()


async def issue_ticket(
    db: Session,
    request: TicketIssueRequest,
    kafka_producer: KafkaProducer,
    *,
    correlation_id: str | None = None,
    source: TicketSource = TicketSource.API,
) -> Ticket:
    """티켓 발급 결과와 artifact/Kafka 경계를 telemetry event로 남긴다."""
    issue_attempt = ticket_metrics.start_issue(source)
    try:
        # reservation 단위 중복 발급은 duplicate 결과로 분리한다.
        existing = db.query(Ticket).filter(Ticket.reservation_id == request.reservationId).first()
        if existing:
            issue_attempt.mark_duplicate()
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

        ticket.qr_url = _upload_ticket_artifact(TicketArtifact.QR, ticket.id, request.reservationId)
        ticket.pdf_url = _upload_ticket_artifact(TicketArtifact.PDF, ticket.id, request.reservationId)

        db.commit()
        db.refresh(ticket)

        payload = _ticket_issued_event(ticket, correlation_id=correlation_id)
        if kafka_producer is not None:
            try:
                await kafka_producer.send_and_wait(
                    settings.ticket_issued_topic,
                    payload,
                    headers=build_producer_headers(correlation_id=correlation_id or payload.get("correlationId")),
                )
            except Exception:
                ticket_metrics.record(
                    TicketEventPublishRecorded(
                        event_type=TicketEventType.TICKET_ISSUED,
                        result=MetricResult.FAILURE,
                    )
                )
                raise
            ticket_metrics.record(
                TicketEventPublishRecorded(
                    event_type=TicketEventType.TICKET_ISSUED,
                    result=MetricResult.SUCCESS,
                )
            )

        issue_attempt.mark_success()
        return ticket
    finally:
        issue_attempt.record()


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


async def handle_payment_approved(db: Session, payload: dict, kafka_producer: KafkaProducer) -> None:
    """결제 승인 이벤트 처리 결과를 소비 metric으로 남긴다."""
    consume_attempt = ticket_metrics.start_event_consume(
        topic=settings.payment_approved_topic,
        event_type=str(payload.get("eventType", "")),
    )
    try:
        event = PaymentApprovedEvent.model_validate(payload)

        processed = db.query(ProcessedEvent).filter(ProcessedEvent.event_id == event.eventId).first()
        if processed:
            consume_attempt.mark_duplicate()
            return

        request = TicketIssueRequest(
            reservationId=event.reservationId,
            userId=event.userId,
            concertId=event.concertId,
            seatId=event.seatId,
        )
        ticket = await issue_ticket(
            db,
            request,
            kafka_producer,
            correlation_id=event.correlationId,
            source=TicketSource.PAYMENT_APPROVED_EVENT,
        )

        db.add(ProcessedEvent(event_id=event.eventId, ticket_id=ticket.id))
        db.commit()
        consume_attempt.mark_success()
    finally:
        consume_attempt.record()


def _upload_ticket_artifact(artifact: TicketArtifact, ticket_id: int, reservation_id: str) -> str | None:
    """QR/PDF 업로드 시간을 artifact별 metric으로 남긴다."""
    attempt = ticket_metrics.start_artifact_upload(artifact)
    try:
        if artifact is TicketArtifact.QR:
            result = s3.upload_qr(ticket_id, reservation_id)
        else:
            result = s3.upload_pdf(ticket_id, reservation_id)
        attempt.mark_success()
        return result
    finally:
        attempt.record()


def _ticket_issued_event(ticket: Ticket, *, correlation_id: str | None = None) -> dict:
    return TicketIssuedEvent(
        eventId=str(uuid4()),
        userId=str(ticket.user_id),
        sourceId=str(ticket.id),
        reservationId=ticket.reservation_id,
        concertId=ticket.concert_id,
        seatId=ticket.seat_id,
        ticketId=str(ticket.id),
        occurredAt=datetime.now(UTC),
        producer=settings.service_name,
        correlationId=correlation_id,
    ).model_dump(mode="json")
