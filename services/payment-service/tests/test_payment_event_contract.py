from uuid import UUID

from contracts.events import PaymentApprovedEvent
from server.ids import deterministic_uuid_string

from app.auth import UserContext
from app.metrics import PaymentEventType
from app.models import Payment
from app.schemas import CreatePaymentRequest
from app.services.payment_events import build_payment_event_draft


def uuid_id(*parts: object) -> str:
    return deterministic_uuid_string("payment-event-contract-test", *parts)


def test_payment_approved_event_payload_matches_consumer_contract() -> None:
    payment_id = uuid_id("payment", 1)
    reservation_id = uuid_id("reservation", 1)
    concert_id = uuid_id("concert", 1)
    seat_id = uuid_id("seat", 1)
    payment = Payment(
        id=payment_id,
        reservation_id=reservation_id,
        concert_id=concert_id,
        user_id="14",
        amount=50000,
        method="CARD",
        status="approved",
    )
    request = CreatePaymentRequest(
        reservationId=reservation_id,
        concertId=concert_id,
        seatId=seat_id,
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
    UUID(draft.event_id)
    UUID(draft.payload["eventId"])
    assert draft.payload["userId"] == "14"
    assert draft.payload["reservationId"] == reservation_id
    assert draft.payload["paymentId"] == payment_id
    assert draft.payload["seatId"] == seat_id
    assert "status" not in draft.payload
    assert PaymentApprovedEvent.model_validate(draft.payload).userId == "14"
