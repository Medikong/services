from collections.abc import Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Final

import anyio
import pytest
from fastapi.testclient import TestClient

from app.db import resources_from_env
from app.main import create_app
from app.messaging import NoopPaymentEventPublisher, handle_order_created_message
from app.models import Payment, PaymentId
from app.store import KnownOrder, PaymentStore
from contracts import OrderCreatedEvent

DEFAULT_ORDER_CREATED: Final = OrderCreatedEvent(
    eventId="evt-order-created-default",
    userId="user-001",
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


class RecordingPaymentEventPublisher:
    def __init__(self) -> None:
        self.published_payments: list[Payment] = []
        self.published_failed_payments: list[Payment] = []

    async def publish_payment_approved(self, payment: Payment) -> None:
        self.published_payments.append(payment)

    async def publish_payment_failed(self, payment: Payment) -> None:
        self.published_failed_payments.append(payment)


@dataclass(frozen=True, slots=True)
class FakeKafkaMessage:
    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None
    value: bytes | None


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
    assert isinstance(resources.event_publisher.current, NoopPaymentEventPublisher)
    assert resources.kafka_bootstrap_servers == "kafka:9092"


def test_approve_mock_payment_returns_approved_payment_and_publishes_event() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-approve-001",
        },
        json={"orderId": "order-001", "amount": 50000},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["orderId"] == "order-001"
    assert body["data"]["userId"] == "user-001"
    assert body["data"]["amount"] == 50000
    assert body["data"]["method"] == "MOCK_CARD"
    assert body["data"]["status"] == "APPROVED"
    assert publisher.published_payments == [
        Payment.model_validate(body["data"]),
    ]


def test_approve_mock_payment_reuses_payment_when_idempotency_key_repeats() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))
    headers = {
        "X-User-Id": "user-001",
        "X-User-Role": "CUSTOMER",
        "Idempotency-Key": "payment-approve-replay-001",
    }
    payload = {"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"}

    # When
    first_response = client.post("/payments/mock-approvals", headers=headers, json=payload)
    second_response = client.post("/payments/mock-approvals", headers=headers, json=payload)

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 201
    assert first_response.json()["data"]["id"] == second_response.json()["data"]["id"]
    assert len(publisher.published_payments) == 1


def test_fail_mock_payment_returns_failed_payment_and_publishes_event() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))

    # When
    response = client.post(
        "/payments/mock-failures",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-fail-001",
        },
        json={"orderId": "order-001", "amount": 50000, "reason": "card_declined"},
    )

    # Then
    assert response.status_code == 201
    body = response.json()
    assert body["data"]["orderId"] == "order-001"
    assert body["data"]["userId"] == "user-001"
    assert body["data"]["amount"] == 50000
    assert body["data"]["method"] == "MOCK_CARD"
    assert body["data"]["status"] == "FAILED"
    assert body["data"]["approvedAt"] is None
    assert body["data"]["failedAt"] is not None
    assert body["data"]["failureReason"] == "card_declined"
    assert publisher.published_failed_payments == [
        Payment.model_validate(body["data"]),
    ]
    assert publisher.published_payments == []


def test_fail_mock_payment_reuses_payment_when_idempotency_key_repeats() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    publisher = RecordingPaymentEventPublisher()
    client = TestClient(create_app(store, publisher))
    headers = {
        "X-User-Id": "user-001",
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
    first_response = client.post("/payments/mock-failures", headers=headers, json=payload)
    second_response = client.post("/payments/mock-failures", headers=headers, json=payload)

    # Then
    assert first_response.status_code == 201
    assert second_response.status_code == 201
    assert first_response.json()["data"]["id"] == second_response.json()["data"]["id"]
    assert len(publisher.published_failed_payments) == 1


def test_get_payment_returns_approved_payment_for_owner_customer() -> None:
    # Given
    store = PaymentStore()
    record_known_order(store)
    client = TestClient(create_app(store))
    create_response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-get-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )
    payment_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/payments/{payment_id}",
        headers={"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"},
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
            "X-User-Id": "user-001",
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": "payment-owner-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )
    payment_id = create_response.json()["data"]["id"]

    # When
    response = client.get(
        f"/payments/{payment_id}",
        headers={"X-User-Id": "user-002", "X-User-Role": "CUSTOMER"},
    )

    # Then
    assert response.status_code == 403


def test_approve_mock_payment_returns_403_when_owner_role_requests_approval() -> None:
    # Given
    client = TestClient(create_app(PaymentStore()))

    # When
    response = client.post(
        "/payments/mock-approvals",
        headers={
            "X-User-Id": "owner-001",
            "X-User-Role": "OWNER",
            "Idempotency-Key": "payment-owner-role-001",
        },
        json={"orderId": "order-001", "amount": 50000, "method": "MOCK_CARD"},
    )

    # Then
    assert response.status_code == 403


def test_order_created_kafka_message_records_known_order_when_payload_is_valid() -> None:
    # Given
    store = PaymentStore()
    event = OrderCreatedEvent(
        eventId="evt-order-created-001",
        userId="user-001",
        sourceId="order-001",
        occurredAt=datetime(2026, 7, 3, 12, 0, tzinfo=UTC),
        producer="order-service",
        orderId="order-001",
        dropId="drop-001",
        productId="product-001",
        quantity=1,
        amount=50000,
        idempotencyKey="order-create-001",
    )
    message = FakeKafkaMessage(
        topic="order.created",
        partition=0,
        offset=1,
        headers=None,
        value=event.model_dump_json().encode("utf-8"),
    )

    # When
    anyio.run(handle_order_created_message, message, store)

    # Then
    known_order = anyio.run(store.get_known_order, "order-001")
    assert known_order == KnownOrder(order_id="order-001", user_id="user-001", amount=50000)
