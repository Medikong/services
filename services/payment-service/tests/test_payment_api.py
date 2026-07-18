from datetime import UTC, datetime
from typing import Final

import anyio
import pytest
from fastapi.testclient import TestClient

from app.db import resources_from_env
from app.main import create_app
from app.store import PaymentStore
from contracts import OrderCreatedEvent

DEFAULT_ORDER_CREATED: Final = OrderCreatedEvent(
    eventId="evt-order-created-default",
    userId="00000000-0000-4000-8000-000000000001",
    sourceId="order-001",
    occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
    producer="order-service",
    orderId="order-001",
    dropId="drop-001",
    productId="product-001",
    quantity=1,
    amount=50000,
    idempotencyKey="order-create-default",
)


def record_known_order(
    store: PaymentStore,
    event: OrderCreatedEvent = DEFAULT_ORDER_CREATED,
) -> None:
    anyio.run(store.record_order_created, event)


def test_healthz_returns_payment_service_identity() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.get("/healthz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ok"
    assert response.json()["service"] == "payment-service"


def test_readyz_returns_ready_payment_checks() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.get("/readyz")

    # Then
    assert response.status_code == 200
    assert response.json()["status"] == "ready"
    assert response.json()["checks"] == {
        "payments": "ok",
        "order_created_handler": "ok",
    }


def test_metrics_exposes_payment_service_readiness_metric() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.get("/metrics")

    # Then
    assert response.status_code == 200
    assert 'service_ready{service_name="payment-service"' in response.text


def test_resources_from_env_defers_kafka_clients_until_lifespan(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    monkeypatch.delenv("DATABASE_URL", raising=False)
    monkeypatch.setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")

    # When
    resources = resources_from_env()

    # Then
    assert isinstance(resources.repository, PaymentStore)
    assert resources.kafka_bootstrap_servers == "kafka:9092"


def test_approve_mock_payment_returns_approved_payment() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-approve-001",
        },
        json={"orderId": "order-001", "amount": 50000},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["orderId"] == "order-001"
    assert body["data"]["userId"] == "00000000-0000-4000-8000-000000000001"
    assert body["data"]["amount"] == 50000
    assert body["data"]["method"] == "MOCK_CARD"
    assert body["data"]["status"] == "APPROVED"


def test_approve_mock_payment_reuses_payment_when_idempotency_key_repeats() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    headers = {
        "X-User-Id": "00000000-0000-4000-8000-000000000001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "payment-approve-replay-001",
    }
    payload = {"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"}

    # When
    first_response = client.post(
        "/payments/mock-approvals", headers=headers, json=payload
    )
    second_response = client.post(
        "/payments/mock-approvals", headers=headers, json=payload
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 201
    assert first_response.json()["data"]["id"] == second_response.json()["data"]["id"]


def test_fail_mock_payment_returns_failed_payment() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-fail-001",
        },
        json={"orderId": "order-001", "amount": 50000, "reason": "card_declined"},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["orderId"] == "order-001"
    assert body["data"]["userId"] == "00000000-0000-4000-8000-000000000001"
    assert body["data"]["amount"] == 50000
    assert body["data"]["method"] == "MOCK_CARD"
    assert body["data"]["status"] == "FAILED"
    assert body["data"]["approvedAt"] is None
    assert body["data"]["failedAt"] is not None
    assert body["data"]["failureReason"] == "card_declined"


def test_fail_mock_payment_reuses_payment_when_idempotency_key_repeats() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    headers = {
        "X-User-Id": "00000000-0000-4000-8000-000000000001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "payment-fail-replay-001",
    }
    payload = {
        "orderId": "order-001",
        "amount": 50000,
        "method": "MOCK_CARD",
        "reason": "card_declined",
    }

    # When
    first_response = client.post(
        "/payments/mock-failures", headers=headers, json=payload
    )
    second_response = client.post(
        "/payments/mock-failures", headers=headers, json=payload
    )

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 201
    assert first_response.json()["data"]["id"] == second_response.json()["data"]["id"]


def test_fail_mock_payment_returns_conflict_when_order_is_already_approved() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    approval_response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-terminal-approval-001",
        },
        json={"orderId": "order-001", "amount": 50000},
    )
    assert approval_response.status_code == 201

    # When
    failure_response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-terminal-failure-001",
        },
        json={"orderId": "order-001", "amount": 50000},
    )

    # Then
    assert failure_response.status_code == 409


def test_get_payment_returns_approved_payment_for_owner_customer() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    create_response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-get-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )
    payment_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/payments/{payment_id}",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert response.status_code == 200
    assert response.json()["data"]["id"] == payment_id
    assert response.json()["data"]["status"] == "APPROVED"


def test_get_payment_returns_403_when_customer_reads_another_customer_payment() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    create_response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-owner-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )
    payment_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/payments/{payment_id}",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000002",
            "X-User-Role": "CUSTOMER",
        },
    )

    # Then
    assert response.status_code == 403


def test_approve_mock_payment_ignores_untrusted_owner_role_header() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "00000000-0000-4000-8000-000000000001",
            "X-User-Role": "OWNER",
            "Idempotency-Key": "payment-owner-role-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )

    # Then
    assert response.status_code == 201
    assert response.json()["data"]["userId"] == (
        "00000000-0000-4000-8000-000000000001"
    )
