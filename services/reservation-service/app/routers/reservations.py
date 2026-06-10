from datetime import UTC, datetime
from typing import Annotated
from uuid import uuid4

from aiokafka.errors import KafkaError
from fastapi import APIRouter, Depends, Request, status
from kafka_utils import build_producer_headers
from metrics import MetricResult

from app import schemas
from app.config import settings
from app.dependencies import get_user_id
from app.kafka import KafkaProducer, get_kafka_producer
from app.metrics import ReservationEventType
from app.metrics.events import ReservationEventPublishRecorded
from app.metrics.recorder import ReservationTelemetryRecorder
from app.routers.dependencies import reservation_command_service, reservation_query_service
from app.services import ReservationCommandService, ReservationQueryService
from app.services.reservations import concert_id_from_request


router = APIRouter()


@router.post("/reservations", status_code=status.HTTP_201_CREATED, response_model=schemas.ReservationResponse)
async def create_reservation(
    request: schemas.CreateReservationRequest,
    http_request: Request,
    reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)],
    kafka_producer: Annotated[KafkaProducer, Depends(get_kafka_producer)],
    user_id: Annotated[str, Depends(get_user_id)],
) -> schemas.ReservationResponse:
    response = reservations.create_reservation(user_id, request)
    payload = _reservation_event_payload(
        event_name="reservation-created",
        response=response,
        source_id=response.id,
        concert_id=concert_id_from_request(request),
        http_request=http_request,
    )
    if kafka_producer is not None:
        telemetry = ReservationTelemetryRecorder()
        try:
            await kafka_producer.send_and_wait(
                settings.reservation_created_topic,
                payload,
                headers=build_producer_headers(correlation_id=payload.get("correlationId")),
            )
        except (KafkaError, RuntimeError):
            telemetry.record(
                ReservationEventPublishRecorded(event_type=ReservationEventType.CREATED, result=MetricResult.FAILURE)
            )
            raise
        telemetry.record(
            ReservationEventPublishRecorded(event_type=ReservationEventType.CREATED, result=MetricResult.SUCCESS)
        )
    return response


@router.get("/reservations/me", response_model=schemas.ReservationListResponse)
def list_my_reservations(
    reservations: Annotated[ReservationQueryService, Depends(reservation_query_service)],
    user_id: Annotated[str, Depends(get_user_id)],
    limit: int = 20,
) -> schemas.ReservationListResponse:
    return reservations.list_my_reservations(user_id, limit)


@router.get("/reservations/{id}", response_model=schemas.ReservationResponse)
def get_reservation(id: str, reservations: Annotated[ReservationQueryService, Depends(reservation_query_service)]) -> schemas.ReservationResponse:
    return reservations.get_reservation(id)


@router.post("/reservations/{id}/cancel", response_model=schemas.ReservationResponse)
def cancel_reservation(id: str, reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)]) -> schemas.ReservationResponse:
    return reservations.cancel_reservation(id)


@router.post("/reservations/{id}/expire", response_model=schemas.ReservationResponse)
async def expire_reservation(
    id: str,
    http_request: Request,
    reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)],
    kafka_producer: Annotated[KafkaProducer, Depends(get_kafka_producer)],
) -> schemas.ReservationResponse:
    response = reservations.expire_reservation(id)
    payload = _reservation_event_payload(
        event_name="reservation-expired",
        response=response,
        source_id=response.id,
        concert_id=None,
        http_request=http_request,
    )
    if kafka_producer is not None:
        telemetry = ReservationTelemetryRecorder()
        try:
            await kafka_producer.send_and_wait(
                settings.reservation_expired_topic,
                payload,
                headers=build_producer_headers(correlation_id=payload.get("correlationId")),
            )
        except (KafkaError, RuntimeError):
            telemetry.record(
                ReservationEventPublishRecorded(event_type=ReservationEventType.EXPIRED, result=MetricResult.FAILURE)
            )
            raise
        telemetry.record(
            ReservationEventPublishRecorded(event_type=ReservationEventType.EXPIRED, result=MetricResult.SUCCESS)
        )
    return response


def _reservation_event_payload(
    *,
    event_name: str,
    response: schemas.ReservationResponse,
    source_id: str,
    concert_id: str | None,
    http_request: Request,
) -> dict:
    return {
        "eventId": f"evt-{uuid4()}",
        "eventType": event_name,
        "userId": _event_user_id(response.userId),
        "sourceId": source_id,
        "reservationId": response.id,
        "concertId": concert_id,
        "seatId": response.seatId,
        "occurredAt": datetime.now(UTC).isoformat(),
        "producer": settings.service_name,
        "correlationId": getattr(http_request.state, "request_id", None) or http_request.headers.get("X-Request-Id"),
    }


def _event_user_id(value: str) -> str:
    return value
