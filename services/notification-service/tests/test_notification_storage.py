from datetime import UTC, datetime

import pytest
from app.store import notification_from_requested_event
from pydantic import ValidationError

from contracts import NotificationRequestedEvent, NotificationType


@pytest.mark.parametrize("notification_type", tuple(NotificationType))
def test_notification_from_requested_event_preserves_typed_outcome(
    notification_type: NotificationType,
) -> None:
    # Given
    event = NotificationRequestedEvent(
        eventId=f"evt-{notification_type.value.lower()}",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 14, tzinfo=UTC),
        producer="order-service",
        notificationId=f"notification-{notification_type.value.lower()}",
        orderId="order-001",
        notificationType=notification_type,
        title="purchase outcome",
        message="purchase lifecycle notification",
    )

    # When
    notification = notification_from_requested_event(event)

    # Then
    assert notification.type is notification_type


def test_notification_requested_event_rejects_unknown_typed_outcome() -> None:
    # Given
    payload = {
        "eventId": "evt-invalid-outcome",
        "userId": "user-001",
        "sourceId": "order-001",
        "occurredAt": "2026-07-14T00:00:00Z",
        "producer": "order-service",
        "notificationId": "notification-invalid-outcome",
        "orderId": "order-001",
        "notificationType": "UNKNOWN_OUTCOME",
        "title": "purchase outcome",
        "message": "purchase lifecycle notification",
    }

    # When / Then
    with pytest.raises(ValidationError):
        NotificationRequestedEvent.model_validate(payload)
