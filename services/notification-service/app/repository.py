from typing import Protocol

from app.models import NotificationId, UserId
from app.store import NotificationPage, RecordNotificationResult
from contracts import NotificationRequestedEvent


class NotificationRepository(Protocol):
    async def is_ready(self) -> bool: ...

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
