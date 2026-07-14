import pytest
from pydantic import ValidationError

from contracts.events import (
    NotificationRequestedEvent,
    OrderConfirmedEvent,
    OrderCreatedEvent,
    PaymentApprovedEvent,
    PaymentFailedEvent,
)


COMMON_PAYLOAD = {
    "eventId": "legacy-event-1",
    "userId": "legacy-user-1",
    "sourceId": "legacy-source-1",
    "occurredAt": "2026-05-13T10:00:00Z",
    "producer": "legacy-producer",
}


def test_legacy_order_created_payload_preserves_type_and_fields() -> None:
    event = OrderCreatedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "order.created",
            "orderId": "legacy-order-1",
            "dropId": "legacy-drop-1",
            "productId": "legacy-product-1",
            "quantity": 2,
            "amount": 50000,
        }
    )

    assert event.eventType == "order.created"
    assert event.schemaVersion == 1
    assert event.orderId == "legacy-order-1"
    assert event.quantity == 2


def test_legacy_payment_approved_payload_preserves_type_and_fields() -> None:
    event = PaymentApprovedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "payment.approved",
            "orderId": "legacy-order-1",
            "paymentId": "legacy-payment-1",
            "amount": 50000,
        }
    )

    assert event.eventType == "payment.approved"
    assert event.schemaVersion == 1
    assert event.paymentId == "legacy-payment-1"
    assert event.amount == 50000


def test_legacy_payment_failed_payload_preserves_type_and_fields() -> None:
    event = PaymentFailedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "payment.failed",
            "orderId": "legacy-order-1",
            "paymentId": "legacy-payment-1",
            "amount": 50000,
        }
    )

    assert event.eventType == "payment.failed"
    assert event.schemaVersion == 1
    assert event.orderId == "legacy-order-1"
    assert event.reason is None


def test_legacy_order_confirmed_payload_preserves_type_and_fields() -> None:
    event = OrderConfirmedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "order.confirmed",
            "orderId": "legacy-order-1",
            "paymentId": "legacy-payment-1",
            "dropId": "legacy-drop-1",
            "productId": "legacy-product-1",
            "quantity": 2,
            "amount": 50000,
        }
    )

    assert event.eventType == "order.confirmed"
    assert event.schemaVersion == 1
    assert event.productId == "legacy-product-1"
    assert event.quantity == 2


def test_legacy_notification_requested_payload_preserves_type_and_fields() -> None:
    event = NotificationRequestedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "notification.requested",
            "notificationId": "legacy-notification-1",
            "orderId": "legacy-order-1",
            "title": "legacy title",
            "message": "legacy message",
        }
    )

    assert event.eventType == "notification.requested"
    assert event.schemaVersion == 1
    assert event.notificationId == "legacy-notification-1"
    assert event.channel == "IN_APP"


def test_legacy_event_rejects_unknown_fields() -> None:
    with pytest.raises(ValidationError):
        OrderCreatedEvent.model_validate(
            COMMON_PAYLOAD
            | {
                "eventType": "order.created",
                "orderId": "legacy-order-1",
                "dropId": "legacy-drop-1",
                "productId": "legacy-product-1",
                "quantity": 2,
                "amount": 50000,
                "unknownKey": "must fail",
            }
        )
