from typing import TypeAlias, assert_never

from bson import ObjectId
from contracts.events import (
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    RESERVATION_CREATED_TOPIC,
    RESERVATION_EXPIRED_TOPIC,
    TICKET_ISSUED_TOPIC,
    PaymentApprovedEvent,
    PaymentFailedEvent,
    ReservationCreatedEvent,
    ReservationExpiredEvent,
    TicketIssuedEvent,
)
from fastapi import HTTPException, status
from metrics import MetricResult
from motor.motor_asyncio import AsyncIOMotorDatabase

from app.auth import UserContext
from app.metrics.events import NotificationReadRecorded
from app.metrics.labels import NotificationRouteKind
from app.metrics.recorder import NotificationTelemetryRecorder
from app.models import notification_to_doc, processed_event_to_doc


notification_metrics = NotificationTelemetryRecorder()
NotificationEvent: TypeAlias = (
    ReservationCreatedEvent
    | ReservationExpiredEvent
    | PaymentApprovedEvent
    | PaymentFailedEvent
    | TicketIssuedEvent
)


def _serialize(doc: dict) -> dict:
    doc["id"] = str(doc.pop("_id"))
    doc["userId"] = doc.pop("user_id")
    doc["sourceId"] = doc.pop("source_id")
    doc["createdAt"] = doc.pop("created_at")
    return doc


async def list_notifications(
    db: AsyncIOMotorDatabase, user: UserContext
) -> list[dict]:
    """알림 목록 조회 결과를 route_kind 단위 metric으로 남긴다."""
    try:
        query = {"user_id": user.user_id}
        cursor = db["notifications"].find(query).sort("_id", -1)
        items = [_serialize(doc) async for doc in cursor]
    except Exception:
        notification_metrics.record(
            NotificationReadRecorded(route_kind=NotificationRouteKind.LIST, result=MetricResult.FAILURE)
        )
        raise
    notification_metrics.record(
        NotificationReadRecorded(route_kind=NotificationRouteKind.LIST, result=MetricResult.SUCCESS)
    )
    return items


async def get_notification(
    db: AsyncIOMotorDatabase, notification_id: str, user: UserContext
) -> dict:
    """알림 상세 조회 결과를 route_kind 단위 metric으로 남긴다."""
    try:
        doc = await db["notifications"].find_one({"_id": ObjectId(notification_id)})
        if doc is None:
            notification_metrics.record(
                NotificationReadRecorded(route_kind=NotificationRouteKind.DETAIL, result=MetricResult.REJECTION)
            )
            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Notification not found")
        if doc["user_id"] != user.user_id:
            notification_metrics.record(
                NotificationReadRecorded(route_kind=NotificationRouteKind.DETAIL, result=MetricResult.REJECTION)
            )
            raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Not allowed")
        result = _serialize(doc)
    except HTTPException:
        raise
    except Exception:
        notification_metrics.record(
            NotificationReadRecorded(route_kind=NotificationRouteKind.DETAIL, result=MetricResult.FAILURE)
        )
        raise
    notification_metrics.record(
        NotificationReadRecorded(route_kind=NotificationRouteKind.DETAIL, result=MetricResult.SUCCESS)
    )
    return result


async def handle_business_event(db: AsyncIOMotorDatabase, payload: dict) -> dict:
    """비즈니스 이벤트 소비와 알림 생성 결과를 metric으로 남긴다."""
    event_type = str(payload.get("eventType", ""))
    attempt = notification_metrics.start_event(topic=event_type, event_type=event_type)
    try:
        event = _parse_business_event(payload)

        processed = await db["processed_events"].find_one({"event_id": event.eventId})
        if processed:
            doc = await db["notifications"].find_one({"_id": ObjectId(processed["notification_id"])})
            if doc:
                attempt.mark_duplicate()
                return _serialize(doc)

        doc = notification_to_doc(
            user_id=event.userId,
            type=event.eventType,
            message=_message_for_event(event),
            status="CREATED",
            source_id=event.sourceId,
            metadata=_metadata_for_event(event),
        )
        result = await db["notifications"].insert_one(doc)
        await db["processed_events"].insert_one(processed_event_to_doc(event.eventId, str(result.inserted_id)))
        doc["_id"] = result.inserted_id
        attempt.mark_success()
        return _serialize(doc)
    finally:
        attempt.record()


def _parse_business_event(payload: dict) -> NotificationEvent:
    match str(payload.get("eventType", "")):
        case event_type if event_type == RESERVATION_CREATED_TOPIC:
            return ReservationCreatedEvent.model_validate(payload)
        case event_type if event_type == RESERVATION_EXPIRED_TOPIC:
            return ReservationExpiredEvent.model_validate(payload)
        case event_type if event_type == PAYMENT_APPROVED_TOPIC:
            return PaymentApprovedEvent.model_validate(payload)
        case event_type if event_type == PAYMENT_FAILED_TOPIC:
            return PaymentFailedEvent.model_validate(payload)
        case event_type if event_type == TICKET_ISSUED_TOPIC:
            return TicketIssuedEvent.model_validate(payload)
        case event_type:
            raise ValueError(f"unsupported business event type: {event_type}")


def _message_for_event(event: NotificationEvent) -> str:
    match event:
        case ReservationCreatedEvent():
            return f"예매가 생성되었습니다. 예매 ID: {event.sourceId}"
        case ReservationExpiredEvent():
            return f"결제 시간이 만료되어 예매가 취소되었습니다. 예매 ID: {event.sourceId}"
        case PaymentApprovedEvent():
            return f"결제가 완료되었습니다. 결제 ID: {event.sourceId}"
        case PaymentFailedEvent():
            return f"결제에 실패하였습니다. 결제 ID: {event.sourceId}"
        case TicketIssuedEvent():
            return f"티켓이 발행되었습니다. 티켓 ID: {event.sourceId}"
        case unreachable:
            assert_never(unreachable)


def _metadata_for_event(event: NotificationEvent) -> dict:
    match event:
        case ReservationCreatedEvent():
            return {
                "reservation_id": event.reservationId,
                "concert_id": event.concertId,
                "seat_id": event.seatId,
                "performance_id": event.performanceId,
            }
        case ReservationExpiredEvent():
            return {
                "reservation_id": event.reservationId,
                "concert_id": event.concertId,
                "seat_id": event.seatId,
                "performance_id": event.performanceId,
            }
        case PaymentApprovedEvent():
            return {
                "payment_id": event.paymentId,
                "reservation_id": event.reservationId,
                "concert_id": event.concertId,
                "seat_id": event.seatId,
            }
        case PaymentFailedEvent():
            return {
                "payment_id": event.paymentId,
                "reservation_id": event.reservationId,
                "concert_id": event.concertId,
                "seat_id": event.seatId,
            }
        case TicketIssuedEvent():
            return {
                "ticket_id": event.ticketId,
                "reservation_id": event.reservationId,
                "concert_id": event.concertId,
                "seat_id": event.seatId,
                "payment_id": event.paymentId,
            }
        case unreachable:
            assert_never(unreachable)
