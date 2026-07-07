from datetime import UTC, datetime
from typing import Final

import anyio
import pytest
from fastapi.testclient import TestClient

from app.db import resources_from_env
from app.main import create_app
from app.models import UserId
from app.store import NotificationStore
from contracts import NotificationRequestedEvent

DEFAULT_NOTIFICATION_REQUESTED: Final = NotificationRequestedEvent(
    eventId="evt-notification-requested-001",
    userId="user-001",
    sourceId="order-001",
    occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
    producer="order-service",
    notificationId="notification-001",
    orderId="order-001",
    title="주문이 확정되었습니다",
    message="DropMong 주문이 정상 처리되었습니다.",
)


def test_healthz_returns_notification_service_identity() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get("/healthz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "notification-service"


def test_healthz_echoes_request_id_header() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get("/healthz", headers={"X-Request-Id": "notification-trace-smoke"})

    # Then
    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "notification-trace-smoke"


def test_readyz_returns_ready_notification_checks() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get("/readyz")

    # Then
    assert response.status_code == 200
    assert response.json()["checks"] == {
        "notifications": "ok",
        "notification_requested_handler": "ok",
    }


def test_metrics_exposes_notification_service_readiness_metric() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert 'service_ready{service_name="notification-service"' in response.text


def test_resources_from_env_defers_kafka_clients_until_lifespan(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")

    # When
    resources = resources_from_env()

    # Then
    assert isinstance(resources.repository, NotificationStore)
    assert resources.kafka_bootstrap_servers == "kafka:9092"
    assert resources.kafka_runtime is None


def test_list_notifications_returns_current_customer_notifications() -> None:
    # Given
    store = NotificationStore()
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    client = TestClient(create_app(store))

    # When
    response = client.get(
        "/notifications",
        headers={"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"},
    )

    # Then
    assert response.status_code == 200
    body = response.json()
    assert body["data"][0]["id"] == "notification-001"
    assert body["data"][0]["orderId"] == "order-001"
    assert body["data"][0]["type"] == "ORDER_CONFIRMED"
    assert body["pageInfo"] == {"nextCursor": None, "hasNext": False}


def test_list_notifications_uses_cursor_for_next_page() -> None:
    # Given
    store = NotificationStore()
    newer_event = DEFAULT_NOTIFICATION_REQUESTED.model_copy(
        update={
            "eventId": "evt-notification-requested-002",
            "notificationId": "notification-002",
            "occurredAt": datetime(2026, 7, 3, 12, 1, tzinfo=UTC),
        },
    )
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    anyio.run(store.record_notification_requested, newer_event)
    client = TestClient(create_app(store))

    # When
    first_response = client.get(
        "/notifications?limit=1",
        headers={"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"},
    )
    first_body = first_response.json()
    second_response = client.get(
        f"/notifications?limit=1&cursor={first_body['pageInfo']['nextCursor']}",
        headers={"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"},
    )

    # Then
    assert first_response.status_code == 200
    assert first_body["data"][0]["id"] == "notification-002"
    assert second_response.status_code == 200
    second_body = second_response.json()
    assert second_body["data"][0]["id"] == "notification-001"
    assert second_body["pageInfo"] == {"nextCursor": None, "hasNext": False}


def test_list_notifications_hides_other_customer_notifications() -> None:
    # Given
    store = NotificationStore()
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    client = TestClient(create_app(store))

    # When
    response = client.get(
        "/notifications",
        headers={"X-User-Id": "user-002", "X-User-Role": "CUSTOMER"},
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"] == []


def test_list_notifications_returns_403_when_owner_role_reads_notifications() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get(
        "/notifications",
        headers={"X-User-Id": "owner-001", "X-User-Role": "OWNER"},
    )

    # Then
    assert response.status_code == 403


def test_record_notification_requested_is_idempotent_by_event_id() -> None:
    # Given
    store = NotificationStore()

    # When
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)

    # Then
    assert len(page.notifications) == 1
