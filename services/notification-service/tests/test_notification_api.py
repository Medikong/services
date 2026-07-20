from datetime import UTC, datetime
from time import monotonic
from typing import Final

import anyio
import pytest
from fastapi.testclient import TestClient

from app.db import resources_from_env
from app.main import create_app
from app.metrics import NotificationMetrics
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
    response = client.get(
        "/healthz", headers={"X-Request-Id": "notification-trace-smoke"}
    )

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


def test_readyz_returns_bounded_503_when_postgres_is_unreachable(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.setenv(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@127.0.0.1:59999/notification_db",
    )
    monkeypatch.delenv("KAFKA_BOOTSTRAP_SERVERS", raising=False)

    # When
    with TestClient(create_app(), raise_server_exceptions=False) as client:
        started_at = monotonic()
        response = client.get("/readyz")
        elapsed_seconds = monotonic() - started_at
        ready_value = _service_ready_value(client.get("/metrics").text)

    # Then
    assert response.status_code == 503
    assert response.json()["status"] == "not_ready"
    assert response.json()["checks"]["notifications"] == "migration_required"
    assert ready_value == 0.0
    assert elapsed_seconds < 3.0


def test_metrics_exposes_notification_service_readiness_metric() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    client.get("/healthz")
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert 'service_name="notification-service"' in response.text
    assert _service_ready_value(response.text) == 0.0
    assert "# TYPE http_server_request_duration_seconds histogram" in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_route_kind="probe"' in response.text
    assert 'http_response_status_code="200"' in response.text

    assert client.get("/readyz").status_code == 200
    assert _service_ready_value(client.get("/metrics").text) == 1.0


def _service_ready_value(metrics_text: str) -> float:
    line = next(
        line for line in metrics_text.splitlines() if line.startswith("service_ready{")
    )
    return float(line.rsplit(" ", maxsplit=1)[1])


def test_metrics_exposes_notification_business_metrics() -> None:
    # Given
    metrics = NotificationMetrics("notification-service", "test", "test")
    metrics.record_created()
    client = TestClient(create_app(NotificationStore(), metrics))

    # When
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert "notification_requested_events_consumed_total" in response.text
    assert "notifications_created_total" in response.text


def test_resources_from_env_defers_kafka_clients_until_lifespan(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")

    # When
    resources = resources_from_env(
        NotificationMetrics("notification-service", "test", "test")
    )

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
        headers={"X-User-Id": "user-001"},
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
        headers={"X-User-Id": "user-001"},
    )
    first_body = first_response.json()
    second_response = client.get(
        f"/notifications?limit=1&cursor={first_body['pageInfo']['nextCursor']}",
        headers={"X-User-Id": "user-001"},
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
        headers={"X-User-Id": "user-002"},
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"] == []


def test_list_notifications_ignores_forged_role_and_email() -> None:
    # Given
    store = NotificationStore()
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    client = TestClient(create_app(store))

    # When
    response = client.get(
        "/notifications",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "ADMIN",
            "X-User-Email": "forged@example.com",
        },
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"][0]["id"] == "notification-001"


def test_list_notifications_returns_422_when_user_id_is_missing() -> None:
    # Given
    client = TestClient(create_app(NotificationStore()))

    # When
    response = client.get(
        "/notifications",
        headers={
            "X-User-Role": "CUSTOMER",
            "X-User-Email": "forged@example.com",
        },
    )

    # Then
    assert response.status_code == 422


def test_record_notification_requested_is_idempotent_by_event_id() -> None:
    # Given
    store = NotificationStore()

    # When
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    anyio.run(store.record_notification_requested, DEFAULT_NOTIFICATION_REQUESTED)
    page = anyio.run(store.list_notifications, UserId("user-001"), 20)

    # Then
    assert len(page.notifications) == 1
