import json
from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime

import anyio

from app.messaging import handle_notification_requested_message
from app.metrics import NotificationMetrics
from app.models import UserId
from app.store import NotificationPage, NotificationStore, RecordNotificationResult
from contracts import NotificationRequestedEvent


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


def test_notification_requested_kafka_message_records_notification() -> None:
    # Given
    store = NotificationStore()
    metrics = NotificationMetrics("notification-service", "test", "test")
    event = NotificationRequestedEvent(
        eventId="evt-notification-requested-kafka-001",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        notificationId="notification-001",
        orderId="order-001",
        title="주문이 확정되었습니다",
        message="DropMong 주문이 정상 처리되었습니다.",
    )
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_notification_requested_message, message, store, metrics)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert page.notifications[0].id == "notification-001"
    assert _metric_value(metrics, "notification_requested_events_consumed_total") == 1
    assert _metric_value(metrics, "notifications_created_total") == 1
    assert _metric_value(metrics, "notification_requested_events_replayed_total") == 0


def test_notification_requested_kafka_message_ignores_invalid_payload() -> None:
    # Given
    store = NotificationStore()
    metrics = NotificationMetrics("notification-service", "test", "test")
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=b'{"eventType":"notification.requested"}',
    )

    # When
    anyio.run(handle_notification_requested_message, message, store, metrics)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert page.notifications == ()
    assert _metric_value(metrics, "notification_requested_events_consumed_total") == 1
    assert _metric_value(metrics, "notification_requested_events_invalid_total") == 1


def test_notification_requested_kafka_message_ignores_payload_invalid_for_notification_model() -> (
    None
):
    # Given
    store = NotificationStore()
    metrics = NotificationMetrics("notification-service", "test", "test")
    event = NotificationRequestedEvent(
        eventId="evt-notification-requested-kafka-002",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        notificationId="notification-001",
        orderId="order-001",
        title="x" * 121,
        message="DropMong 주문이 정상 처리되었습니다.",
    )
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_notification_requested_message, message, store, metrics)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert page.notifications == ()
    assert _metric_value(metrics, "notification_requested_events_consumed_total") == 1
    assert _metric_value(metrics, "notification_requested_events_invalid_total") == 1


def test_notification_requested_kafka_message_counts_replay_without_duplicate() -> None:
    # Given
    store = NotificationStore()
    metrics = NotificationMetrics("notification-service", "test", "test")
    event = NotificationRequestedEvent(
        eventId="evt-notification-replay-001",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        notificationId="notification-replay-001",
        orderId="order-001",
        title="주문이 확정되었습니다",
        message="DropMong 주문이 정상 처리되었습니다.",
    )
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_notification_requested_message, message, store, metrics)
    anyio.run(handle_notification_requested_message, message, store, metrics)
    anyio.run(handle_notification_requested_message, message, store, metrics)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert len(page.notifications) == 1
    assert _metric_value(metrics, "notification_requested_events_consumed_total") == 3
    assert _metric_value(metrics, "notifications_created_total") == 1
    assert _metric_value(metrics, "notification_requested_events_replayed_total") == 2


class RejectingNotificationRepository:
    async def record_notification_requested(
        self,
        event: NotificationRequestedEvent,
    ) -> RecordNotificationResult:
        raise AssertionError(f"repository should not receive {event.eventId}")

    async def list_notifications(
        self,
        user_id: UserId,
        limit: int,
        cursor: str | None = None,
    ) -> NotificationPage:
        raise AssertionError(f"repository should not list {user_id}:{limit}:{cursor}")


def test_notification_requested_kafka_message_ignores_event_id_too_long_for_storage() -> (
    None
):
    # Given
    payload = {
        "eventId": "e" * 129,
        "eventType": "notification.requested",
        "userId": "user-001",
        "sourceId": "order-001",
        "occurredAt": "2026-07-03T12:00:00Z",
        "producer": "order-service",
        "notificationId": "notification-001",
        "orderId": "order-001",
        "title": "주문이 확정되었습니다",
        "message": "DropMong 주문이 정상 처리되었습니다.",
    }
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=json.dumps(payload).encode("utf-8"),
    )

    # When / Then
    anyio.run(
        handle_notification_requested_message,
        message,
        RejectingNotificationRepository(),
        NotificationMetrics("notification-service", "test", "test"),
    )


def _metric_value(metrics: NotificationMetrics, name: str) -> int:
    sample = next(
        line
        for line in metrics.render().splitlines()
        if line.startswith(f"{name}{'{'}")
    )
    return int(sample.rsplit(" ", maxsplit=1)[1])
