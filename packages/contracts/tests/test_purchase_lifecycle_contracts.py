from itertools import product

import pytest
from pydantic import ValidationError

import contracts.events as event_contracts


COMMON_PAYLOAD = {
    "eventId": "event-1",
    "userId": "user-1",
    "sourceId": "source-1",
    "occurredAt": "2026-07-14T10:00:00Z",
    "producer": "contract-test",
}

NEW_EVENT_CASES = (
    (
        "OrderExpiredEvent",
        "order.expired",
        {
            "orderId": "order-1",
            "dropId": "drop-1",
            "productId": "product-1",
            "quantity": 2,
            "amount": 50000,
        },
    ),
    (
        "InventoryChangedEvent",
        "inventory.changed",
        {
            "dropId": "drop-1",
            "productId": "product-1",
            "totalQuantity": 42,
            "reservedQuantity": 2,
            "soldQuantity": 4,
            "remainingQuantity": 36,
            "inventoryVersion": 3,
        },
    ),
    (
        "RefundRequestedEvent",
        "refund.requested",
        {
            "refundId": "refund-1",
            "orderId": "order-1",
            "paymentId": "payment-1",
            "amount": 50000,
            "reason": "customer_request",
        },
    ),
    (
        "RefundCompletedEvent",
        "refund.completed",
        {
            "refundId": "refund-1",
            "orderId": "order-1",
            "paymentId": "payment-1",
            "amount": 50000,
        },
    ),
    (
        "RefundFailedEvent",
        "refund.failed",
        {
            "refundId": "refund-1",
            "orderId": "order-1",
            "paymentId": "payment-1",
            "amount": 50000,
            "reason": "provider_unavailable",
        },
    ),
)


def test_new_event_topics_are_stable() -> None:
    assert getattr(event_contracts, "ORDER_EXPIRED_TOPIC", None) == "order.expired"
    assert getattr(event_contracts, "INVENTORY_CHANGED_TOPIC", None) == (
        "inventory.changed"
    )
    assert getattr(event_contracts, "REFUND_REQUESTED_TOPIC", None) == (
        "refund.requested"
    )
    assert getattr(event_contracts, "REFUND_COMPLETED_TOPIC", None) == (
        "refund.completed"
    )
    assert getattr(event_contracts, "REFUND_FAILED_TOPIC", None) == "refund.failed"


@pytest.mark.parametrize(("model_name", "event_type", "fields"), NEW_EVENT_CASES)
def test_new_event_round_trip_preserves_type_and_schema_version(
    model_name: str,
    event_type: str,
    fields: dict[str, str | int],
) -> None:
    model = getattr(event_contracts, model_name, None)
    assert model is not None, f"missing event contract: {model_name}"
    event = model.model_validate(COMMON_PAYLOAD | fields | {"eventType": event_type})

    restored = model.model_validate_json(event.model_dump_json())

    assert restored == event
    assert restored.eventType == event_type
    assert restored.schemaVersion == 1


def test_new_event_rejects_unknown_fields() -> None:
    model = getattr(event_contracts, "RefundRequestedEvent", None)
    assert model is not None, "missing event contract: RefundRequestedEvent"

    with pytest.raises(ValidationError):
        model.model_validate(
            COMMON_PAYLOAD
            | {
                "eventType": "refund.requested",
                "refundId": "refund-1",
                "orderId": "order-1",
                "paymentId": "payment-1",
                "amount": 50000,
                "reason": "customer_request",
                "unknownKey": "must fail",
            }
        )


def test_notification_type_is_typed_and_defaults_for_legacy_payload() -> None:
    event = event_contracts.NotificationRequestedEvent.model_validate(
        COMMON_PAYLOAD
        | {
            "eventType": "notification.requested",
            "notificationId": "notification-1",
            "orderId": "order-1",
            "title": "Order confirmed",
            "message": "Your order was confirmed.",
        }
    )

    assert str(event.notificationType) == "ORDER_CONFIRMED"


def test_notification_type_rejects_unknown_value() -> None:
    with pytest.raises(ValidationError):
        event_contracts.NotificationRequestedEvent.model_validate(
            COMMON_PAYLOAD
            | {
                "eventType": "notification.requested",
                "notificationId": "notification-1",
                "orderId": "order-1",
                "notificationType": "NOT_A_NOTIFICATION",
                "title": "Unknown",
                "message": "Unknown notification.",
            }
        )


def test_order_lifecycle_allows_only_declared_transitions() -> None:
    order_status = getattr(event_contracts, "OrderStatus", None)
    transition_trigger = getattr(event_contracts, "OrderTransitionTrigger", None)
    is_allowed = getattr(event_contracts, "is_order_transition_allowed", None)
    assert order_status is not None, "missing OrderStatus contract"
    assert transition_trigger is not None, "missing OrderTransitionTrigger contract"
    assert is_allowed is not None, "missing order transition contract"
    expected = {
        (
            order_status.PENDING_PAYMENT,
            order_status.CONFIRMED,
            transition_trigger.PAYMENT_APPROVED,
        ),
        (
            order_status.PENDING_PAYMENT,
            order_status.PAYMENT_FAILED,
            transition_trigger.PAYMENT_FAILED,
        ),
        (
            order_status.PENDING_PAYMENT,
            order_status.EXPIRED,
            transition_trigger.PAYMENT_EXPIRED,
        ),
        (
            order_status.CONFIRMED,
            order_status.CANCEL_PENDING,
            transition_trigger.CANCELLATION_REQUESTED,
        ),
        (
            order_status.CANCEL_PENDING,
            order_status.CANCELED,
            transition_trigger.REFUND_COMPLETED,
        ),
    }
    actual = {
        transition
        for transition in product(order_status, order_status, transition_trigger)
        if is_allowed(*transition)
    }

    assert actual == expected


def test_order_lifecycle_rejects_invalid_enum_and_refund_failure_transition() -> None:
    order_status = getattr(event_contracts, "OrderStatus", None)
    transition_trigger = getattr(event_contracts, "OrderTransitionTrigger", None)
    is_allowed = getattr(event_contracts, "is_order_transition_allowed", None)
    assert order_status is not None
    assert transition_trigger is not None
    assert is_allowed is not None

    with pytest.raises(ValueError):
        order_status("SHIPPED")
    assert not is_allowed(
        order_status.CANCEL_PENDING,
        order_status.CANCELED,
        transition_trigger.REFUND_FAILED,
    )
