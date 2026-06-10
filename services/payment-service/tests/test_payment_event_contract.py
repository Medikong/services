from app.auth import UserContext
from app.metrics import PaymentEventType
from app.models import Payment
from app.schemas import CreatePaymentRequest
from app.services.payment_events import build_payment_event_draft


def test_payment_approved_event_payload_matches_consumer_contract() -> None:
    payment = Payment(
        id="payment-1",
        reservation_id="reservation-1",
        concert_id="concert-1",
        user_id="14",
        amount=50000,
        method="CARD",
        status="approved",
    )
    request = CreatePaymentRequest(
        reservationId="reservation-1",
        concertId="concert-1",
        seatId="seat-A1",
        amount=50000,
        method="CARD",
    )
    user = UserContext(
        user_id="14",
        email="customer@example.com",
        role="CUSTOMER",
        token_id="token-14",
    )

    draft = build_payment_event_draft(
        event_type=PaymentEventType.APPROVED,
        payment=payment,
        request_body=request,
        user=user,
        correlation_id="corr-1",
    )

    assert draft.payload["eventType"] == "payment-approved"
    assert draft.payload["userId"] == "14"
    assert draft.payload["reservationId"] == "reservation-1"
    assert draft.payload["paymentId"] == "payment-1"
    assert draft.payload["seatId"] == "seat-A1"
    assert "status" not in draft.payload
