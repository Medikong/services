from typing import Protocol

from contracts import NotificationRequestedEvent

from app.models import NotificationId, UserId
from app.store import NotificationPage, RecordNotificationResult


class NotificationRepository(Protocol):
    async def record_notification_requested(
        self,
        event: NotificationRequestedEvent,
    ) -> RecordNotificationResult: ...

    async def list_notifications(
        self,
        user_id: UserId,
        limit: int,
        cursor: NotificationId | None = None,
    ) -> NotificationPage: ...
