from collections.abc import AsyncIterator, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime

import anyio
import pytest

from app.messaging import NotificationRequestedConsumer
from app.metrics import NotificationMetrics
from app.models import UserId
from app.store import NotificationStore, RecordNotificationResult
from contracts import NotificationRequestedEvent


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


class FakeKafkaConsumer:
    def __init__(self, messages: tuple[FakeKafkaMessage, ...]) -> None:
        self._messages = messages
        self.commit_count = 0

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def commit(self) -> None:
        self.commit_count += 1

    async def __aiter__(self) -> AsyncIterator[FakeKafkaMessage]:
        for message in self._messages:
            yield message


class RepositoryUnavailableError(RuntimeError):
    pass


class FailingNotificationRepository(NotificationStore):
    async def record_notification_requested(
        self,
        event: NotificationRequestedEvent,
    ) -> RecordNotificationResult:
        raise RepositoryUnavailableError(f"database unavailable for {event.eventId}")


def test_consumer_commits_only_after_notification_is_persisted() -> None:
    # Given
    message = _message(_event("evt-notification-manual-commit"))
    client = FakeKafkaConsumer((message,))
    store = NotificationStore()

    # When
    anyio.run(NotificationRequestedConsumer(client, store, _metrics()).run)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert len(page.notifications) == 1
    assert client.commit_count == 1


def test_consumer_commits_poison_payload_without_business_row() -> None:
    # Given
    message = FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=b'{"eventType":"notification.requested"}',
    )
    client = FakeKafkaConsumer((message,))
    store = NotificationStore()

    # When
    anyio.run(NotificationRequestedConsumer(client, store, _metrics()).run)

    # Then
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)
    assert page.notifications == ()
    assert client.commit_count == 1


def test_consumer_leaves_offset_uncommitted_when_repository_fails() -> None:
    # Given
    client = FakeKafkaConsumer(
        (_message(_event("evt-notification-repository-failure")),)
    )
    consumer = NotificationRequestedConsumer(
        client,
        FailingNotificationRepository(),
        _metrics(),
    )

    # When / Then
    with pytest.raises(RepositoryUnavailableError, match="database unavailable"):
        anyio.run(consumer.run)
    assert client.commit_count == 0


def _event(event_id: str) -> NotificationRequestedEvent:
    return NotificationRequestedEvent(
        eventId=event_id,
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        notificationId="notification-order_confirmed-order-001",
        orderId="order-001",
        title="주문이 확정되었습니다",
        message="DropMong 주문이 정상 처리되었습니다.",
    )


def _message(event: NotificationRequestedEvent) -> FakeKafkaMessage:
    return FakeKafkaMessage(
        topic="notification.requested",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )


def _metrics() -> NotificationMetrics:
    return NotificationMetrics("notification-service", "test", "test")
