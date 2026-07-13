from datetime import UTC, datetime

from app.events import payment_approved_event, payment_failed_event
from app.models import Payment, PaymentMethod, PaymentStatus


def test_payment_approved_event_uses_order_id_as_purchase_correlation() -> None:
    payment = approved_payment()

    event = payment_approved_event(payment)

    assert event.correlationId == payment.orderId


def test_payment_failed_event_uses_order_id_as_purchase_correlation() -> None:
    payment = failed_payment()

    event = payment_failed_event(payment)

    assert event.correlationId == payment.orderId


def approved_payment() -> Payment:
    occurred_at = datetime(2026, 7, 13, tzinfo=UTC)
    return Payment(
        id="payment-001",
        orderId="order-001",
        userId="user-001",
        amount=50000,
        method=PaymentMethod.MOCK_CARD,
        status=PaymentStatus.APPROVED,
        createdAt=occurred_at,
        approvedAt=occurred_at,
    )


def failed_payment() -> Payment:
    occurred_at = datetime(2026, 7, 13, tzinfo=UTC)
    return Payment(
        id="payment-002",
        orderId="order-001",
        userId="user-001",
        amount=50000,
        method=PaymentMethod.MOCK_CARD,
        status=PaymentStatus.FAILED,
        createdAt=occurred_at,
        failedAt=occurred_at,
        failureReason="card_declined",
    )
