from datetime import datetime, timezone


def notification_to_doc(
    user_id: str,
    type: str,
    message: str,
    status: str,
    source_id: str,
    metadata: dict | None = None,
) -> dict:
    return {
        "user_id": user_id,
        "type": type,
        "message": message,
        "status": status,
        "source_id": source_id,
        "metadata": metadata or {},
        "created_at": datetime.now(timezone.utc),
    }


def processed_event_to_doc(event_id: str, notification_id: str) -> dict:
    return {
        "event_id": event_id,
        "notification_id": notification_id,
        "created_at": datetime.now(timezone.utc),
    }
