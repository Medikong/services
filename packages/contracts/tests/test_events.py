import pytest
from pydantic import ValidationError

from contracts.events import (
    NOTIFICATION_REQUESTED_TOPIC,
    ORDER_CONFIRMED_TOPIC,
    ORDER_CREATED_TOPIC,
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    NotificationRequestedEvent,
    OrderConfirmedEvent,
    OrderCreatedEvent,
    PaymentApprovedEvent,
    PaymentFailedEvent,
)


EVENT_ID = "f5f728b8-2f19-5d0c-a5fd-f30e6b02ef37"
PAYMENT_APPROVED_EVENT_ID = "bec580c3-56d9-56e6-ad46-e578b469113c"
SOURCE_ID = "437a1f19-9e4f-553f-8d65-c7c38c31f9f7"
ORDER_ID = "6d50fe99-0797-532e-81c5-ddf7d1d0db68"
DROP_ID = "89e11045-1c65-5685-9604-328d9012fda2"
PRODUCT_ID = "5bc60aa7-7c55-5e06-9d32-17da50ee061b"
PAYMENT_ID = "cceae4e4-ced3-5b24-9423-c3fc323a170a"
NOTIFICATION_ID = "7fd695a1-831a-5092-a736-b3f9d1e828a2"
PAYMENT_EVENT_TYPES = (
    (PaymentApprovedEvent, "payment.approved"),
    (PaymentFailedEvent, "payment.failed"),
)
OUTBOX_ORDER_EVENT_TYPES = (
    (
        OrderCreatedEvent,
        "order.created",
        {
            "dropId": DROP_ID,
            "productId": PRODUCT_ID,
            "quantity": 1,
            "amount": 50000,
        },
    ),
    (
        NotificationRequestedEvent,
        "notification.requested",
        {
            "notificationId": NOTIFICATION_ID,
            "title": "주문이 확정되었습니다",
            "message": "DropMong 주문이 정상 처리되었습니다.",
        },
    ),
)


def test_event_topics_are_stable() -> None:
    assert ORDER_CREATED_TOPIC == "order.created"
    assert PAYMENT_APPROVED_TOPIC == "payment.approved"
    assert PAYMENT_FAILED_TOPIC == "payment.failed"
    assert ORDER_CONFIRMED_TOPIC == "order.confirmed"
    assert NOTIFICATION_REQUESTED_TOPIC == "notification.requested"


def test_payment_approved_event_matches_order_confirmation_input() -> None:
    event = PaymentApprovedEvent.model_validate(
        {
            "eventId": PAYMENT_APPROVED_EVENT_ID,
            "eventType": "payment.approved",
            "userId": "1",
            "sourceId": PAYMENT_ID,
            "orderId": ORDER_ID,
            "paymentId": PAYMENT_ID,
            "amount": 50000,
            "occurredAt": "2026-05-13T10:00:00Z",
            "producer": "payment-service",
            "correlationId": "corr-1",
        }
    )

    assert event.userId == "1"
    assert event.orderId == ORDER_ID
    assert event.paymentId == PAYMENT_ID


def test_all_purchase_flow_events_accept_minimum_payloads() -> None:
    common = {
        "eventId": EVENT_ID,
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "contract-test",
    }

    OrderCreatedEvent.model_validate(
        common
        | {
            "eventType": "order.created",
            "orderId": ORDER_ID,
            "dropId": DROP_ID,
            "productId": PRODUCT_ID,
            "quantity": 1,
            "amount": 50000,
        }
    )
    PaymentFailedEvent.model_validate(
        common
        | {
            "eventType": "payment.failed",
            "orderId": ORDER_ID,
            "paymentId": PAYMENT_ID,
            "amount": 50000,
        }
    )
    OrderConfirmedEvent.model_validate(
        common
        | {
            "eventType": "order.confirmed",
            "orderId": ORDER_ID,
            "paymentId": PAYMENT_ID,
            "dropId": DROP_ID,
            "productId": PRODUCT_ID,
            "quantity": 1,
            "amount": 50000,
        }
    )
    NotificationRequestedEvent.model_validate(
        common
        | {
            "eventType": "notification.requested",
            "notificationId": NOTIFICATION_ID,
            "orderId": ORDER_ID,
            "title": "주문이 확정되었습니다",
            "message": "DropMong 주문이 정상 처리되었습니다.",
        }
    )


def test_business_event_rejects_event_id_too_long_for_storage() -> None:
    with pytest.raises(ValidationError):
        NotificationRequestedEvent.model_validate(
            {
                "eventId": "e" * 129,
                "eventType": "notification.requested",
                "userId": "1",
                "sourceId": SOURCE_ID,
                "occurredAt": "2026-05-13T10:00:00Z",
                "producer": "contract-test",
                "notificationId": NOTIFICATION_ID,
                "orderId": ORDER_ID,
                "title": "주문이 확정되었습니다",
                "message": "DropMong 주문이 정상 처리되었습니다.",
            }
        )


@pytest.mark.parametrize(("event_class", "event_type"), PAYMENT_EVENT_TYPES)
@pytest.mark.parametrize("field", ("orderId", "paymentId"))
def test_payment_event_ids_accept_database_boundary(
    event_class: type[PaymentApprovedEvent] | type[PaymentFailedEvent],
    event_type: str,
    field: str,
) -> None:
    payload = {
        "eventId": EVENT_ID,
        "eventType": event_type,
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "payment-service",
        "orderId": ORDER_ID,
        "paymentId": PAYMENT_ID,
        "amount": 50000,
        field: "x" * 64,
    }

    event = event_class.model_validate(payload)

    assert getattr(event, field) == "x" * 64


@pytest.mark.parametrize(("event_class", "event_type"), PAYMENT_EVENT_TYPES)
@pytest.mark.parametrize("field", ("orderId", "paymentId"))
def test_payment_event_ids_reject_values_larger_than_database_boundary(
    event_class: type[PaymentApprovedEvent] | type[PaymentFailedEvent],
    event_type: str,
    field: str,
) -> None:
    payload = {
        "eventId": EVENT_ID,
        "eventType": event_type,
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "payment-service",
        "orderId": ORDER_ID,
        "paymentId": PAYMENT_ID,
        "amount": 50000,
        field: "x" * 65,
    }

    with pytest.raises(ValidationError) as error:
        event_class.model_validate(payload)

    assert error.value.errors()[0]["loc"] == (field,)
    assert error.value.errors()[0]["type"] == "string_too_long"


@pytest.mark.parametrize(
    ("event_class", "event_type", "event_fields"),
    OUTBOX_ORDER_EVENT_TYPES,
)
@pytest.mark.parametrize("length", (64, 65))
def test_order_outbox_event_ids_match_database_boundary(
    event_class: type[OrderCreatedEvent] | type[NotificationRequestedEvent],
    event_type: str,
    event_fields: dict[str, str | int],
    length: int,
) -> None:
    payload = {
        "eventId": EVENT_ID,
        "eventType": event_type,
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "order-service",
        "orderId": "o" * length,
    } | event_fields

    if length == 64:
        event = event_class.model_validate(payload)
        assert event.orderId == "o" * 64
        return

    with pytest.raises(ValidationError) as error:
        event_class.model_validate(payload)

    assert error.value.errors()[0]["loc"] == ("orderId",)
    assert error.value.errors()[0]["type"] == "string_too_long"


def test_order_created_order_id_matches_payment_storage_limit() -> None:
    payload = {
        "eventId": EVENT_ID,
        "eventType": "order.created",
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "contract-test",
        "dropId": DROP_ID,
        "productId": PRODUCT_ID,
        "quantity": 1,
        "amount": 50000,
    }

    event = OrderCreatedEvent.model_validate(payload | {"orderId": "o" * 64})

    assert len(event.orderId) == 64
    with pytest.raises(ValidationError):
        _ = OrderCreatedEvent.model_validate(payload | {"orderId": "o" * 65})
