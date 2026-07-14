from dataclasses import dataclass
from typing import assert_never

from app.models import Notification, NotificationId, OrderId, PageInfo, UserId
from contracts import NotificationRequestedEvent


@dataclass(frozen=True, slots=True)
class NotificationPage:
    notifications: tuple[Notification, ...]
    page_info: PageInfo


@dataclass(frozen=True, slots=True)
class NotificationRecorded:
    notification: Notification


@dataclass(frozen=True, slots=True)
class NotificationAlreadyRecorded:
    notification: Notification


type RecordNotificationResult = NotificationRecorded | NotificationAlreadyRecorded


class NotificationStore:
    def __init__(self) -> None:
        self._notifications: dict[NotificationId, Notification] = {}
        self._event_index: dict[str, NotificationId] = {}

    async def is_ready(self) -> bool:
        return True

    async def record_notification_requested(
        self,
        event: NotificationRequestedEvent,
    ) -> RecordNotificationResult:
        existing_id = self._event_index.get(event.eventId)
        if existing_id is not None:
            return NotificationAlreadyRecorded(
                notification=self._notifications[existing_id]
            )

        notification = notification_from_requested_event(event)
        notification_id = NotificationId(notification.id)
        self._notifications[notification_id] = notification
        self._event_index[event.eventId] = notification_id
        return NotificationRecorded(notification=notification)

    async def list_notifications(
        self,
        user_id: UserId,
        limit: int,
        cursor: NotificationId | None = None,
    ) -> NotificationPage:
        selected = tuple(
            sorted(
                (
                    notification
                    for notification in self._notifications.values()
                    if notification.userId == user_id
                ),
                key=lambda notification: (notification.createdAt, notification.id),
                reverse=True,
            ),
        )
        if cursor is not None:
            selected = notifications_after_cursor(selected, cursor)
        return page_from_notifications(selected, limit)


def notification_from_requested_event(
    event: NotificationRequestedEvent,
) -> Notification:
    return Notification(
        id=NotificationId(event.notificationId),
        userId=UserId(event.userId),
        orderId=OrderId(event.orderId),
        type=event.notificationType,
        title=event.title,
        message=event.message,
        createdAt=event.occurredAt,
        read=False,
    )


def page_from_notifications(
    notifications: tuple[Notification, ...],
    limit: int,
) -> NotificationPage:
    page = notifications[:limit]
    has_next = len(notifications) > limit
    next_cursor = page[-1].id if has_next and page else None
    return NotificationPage(
        notifications=page,
        page_info=PageInfo(nextCursor=next_cursor, hasNext=has_next),
    )


def notifications_after_cursor(
    notifications: tuple[Notification, ...],
    cursor: NotificationId,
) -> tuple[Notification, ...]:
    for index, notification in enumerate(notifications):
        if notification.id == cursor:
            return notifications[index + 1 :]
    return ()


def recorded_notification(result: RecordNotificationResult) -> Notification:
    match result:
        case NotificationRecorded(notification=notification):
            return notification
        case NotificationAlreadyRecorded(notification=notification):
            return notification
        case unreachable:
            assert_never(unreachable)
