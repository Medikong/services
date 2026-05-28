from bson import ObjectId
from fastapi import HTTPException, status
from motor.motor_asyncio import AsyncIOMotorDatabase

from app.auth import UserContext
from app.models import notification_to_doc, processed_event_to_doc
from app.schemas import BusinessEvent


def _serialize(doc: dict) -> dict:
    doc["id"] = str(doc.pop("_id"))
    doc["userId"] = doc.pop("user_id")
    doc["sourceId"] = doc.pop("source_id")
    doc["createdAt"] = doc.pop("created_at")
    return doc


async def list_notifications(
    db: AsyncIOMotorDatabase, user: UserContext
) -> list[dict]:
    query = {"user_id": user.user_id}
    cursor = db["notifications"].find(query).sort("_id", -1)
    return [_serialize(doc) async for doc in cursor]


async def get_notification(
    db: AsyncIOMotorDatabase, notification_id: str, user: UserContext
) -> dict:
    doc = await db["notifications"].find_one({"_id": ObjectId(notification_id)})
    if doc is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Notification not found"
        )
    if doc["user_id"] != user.user_id:
        raise HTTPException(
            status_code=status.HTTP_403_FORBIDDEN,
            detail="Not allowed"
        )
    return _serialize(doc)


async def handle_business_event(db: AsyncIOMotorDatabase, payload: dict) -> dict:
    event = BusinessEvent.model_validate(payload)

    # idempotency: 이미 처리된 이벤트는 중복 처리하지 않음
    processed = await db["processed_events"].find_one({"event_id": event.eventId})
    if processed:
        doc = await db["notifications"].find_one(
            {"_id": ObjectId(processed["notification_id"])}
        )
        if doc:
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
    await db["processed_events"].insert_one(
        processed_event_to_doc(event.eventId, str(result.inserted_id))
    )
    doc["_id"] = result.inserted_id
    return _serialize(doc)


def _message_for_event(event: BusinessEvent) -> str:
    if event.eventType == "reservation-created":
        return f"예매가 생성되었습니다. 예매 ID: {event.sourceId}"
    if event.eventType == "reservation-expired":
        return f"결제 시간이 만료되어 예매가 취소되었습니다. 예매 ID: {event.sourceId}"
    if event.eventType == "payment-approved":
        return f"결제가 완료되었습니다. 결제 ID: {event.sourceId}"
    if event.eventType == "payment-failed":
        return f"결제에 실패하였습니다. 결제 ID: {event.sourceId}"
    if event.eventType == "ticket-issued":
        return f"티켓이 발행되었습니다. 티켓 ID: {event.sourceId}"
    return f"새 알림이 도착했습니다. 이벤트: {event.eventType}"


def _metadata_for_event(event: BusinessEvent) -> dict:
    if event.eventType == "reservation-created":
        return {"reservation_id": event.sourceId, "concert_id": event.concertId}
    if event.eventType == "reservation-expired":
        return {"reservation_id": event.sourceId}
    if event.eventType == "payment-approved":
        return {"payment_id": event.sourceId, "reservation_id": event.reservationId}
    if event.eventType == "payment-failed":
        return {"payment_id": event.sourceId, "reservation_id": event.reservationId}
    if event.eventType == "ticket-issued":
        return {"ticket_id": event.sourceId, "reservation_id": event.reservationId}
    return {}
