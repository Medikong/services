from contracts.events import (
    PAYMENT_APPROVED_TOPIC,
    PAYMENT_FAILED_TOPIC,
    RESERVATION_CREATED_TOPIC,
    RESERVATION_EXPIRED_TOPIC,
    TICKET_ISSUED_TOPIC,
    PaymentApprovedEvent,
    PaymentFailedEvent,
    ReservationCreatedEvent,
    ReservationExpiredEvent,
    TicketIssuedEvent,
)


EVENT_ID = "f5f728b8-2f19-5d0c-a5fd-f30e6b02ef37"
PAYMENT_APPROVED_EVENT_ID = "bec580c3-56d9-56e6-ad46-e578b469113c"
SOURCE_ID = "437a1f19-9e4f-553f-8d65-c7c38c31f9f7"
RESERVATION_ID = "6d50fe99-0797-532e-81c5-ddf7d1d0db68"
CONCERT_ID = "89e11045-1c65-5685-9604-328d9012fda2"
SHOWTIME_ID = "5bc60aa7-7c55-5e06-9d32-17da50ee061b"
SEAT_ID = "1c7c9994-81e7-55f2-8d57-0169e7ae0ec0"
PAYMENT_ID = "cceae4e4-ced3-5b24-9423-c3fc323a170a"
TICKET_ID = "7fd695a1-831a-5092-a736-b3f9d1e828a2"


def test_event_topics_are_stable() -> None:
    assert RESERVATION_CREATED_TOPIC == "reservation-created"
    assert RESERVATION_EXPIRED_TOPIC == "reservation-expired"
    assert PAYMENT_APPROVED_TOPIC == "payment-approved"
    assert PAYMENT_FAILED_TOPIC == "payment-failed"
    assert TICKET_ISSUED_TOPIC == "ticket-issued"


def test_payment_approved_event_matches_ticket_issue_input() -> None:
    event = PaymentApprovedEvent.model_validate(
        {
            "eventId": PAYMENT_APPROVED_EVENT_ID,
            "eventType": "payment-approved",
            "userId": "1",
            "sourceId": PAYMENT_ID,
            "reservationId": RESERVATION_ID,
            "concertId": CONCERT_ID,
            "performanceId": SHOWTIME_ID,
            "seatId": SEAT_ID,
            "paymentId": PAYMENT_ID,
            "amount": 50000,
            "occurredAt": "2026-05-13T10:00:00Z",
            "producer": "payment-service",
            "correlationId": "corr-1",
        }
    )

    assert event.userId == "1"
    assert event.reservationId == RESERVATION_ID
    assert event.concertId == CONCERT_ID
    assert event.seatId == SEAT_ID


def test_all_reservation_flow_events_accept_minimum_payloads() -> None:
    common = {
        "eventId": EVENT_ID,
        "userId": "1",
        "sourceId": SOURCE_ID,
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "contract-test",
    }

    ReservationCreatedEvent.model_validate(
        common
        | {
            "eventType": "reservation-created",
            "reservationId": RESERVATION_ID,
            "concertId": CONCERT_ID,
            "seatId": SEAT_ID,
        }
    )
    ReservationExpiredEvent.model_validate(
        common
        | {
            "eventType": "reservation-expired",
            "reservationId": RESERVATION_ID,
            "concertId": CONCERT_ID,
            "seatId": SEAT_ID,
        }
    )
    PaymentFailedEvent.model_validate(
        common
        | {
            "eventType": "payment-failed",
            "reservationId": RESERVATION_ID,
            "concertId": CONCERT_ID,
            "seatId": SEAT_ID,
            "paymentId": PAYMENT_ID,
            "amount": 50000,
        }
    )
    TicketIssuedEvent.model_validate(
        common
        | {
            "eventType": "ticket-issued",
            "reservationId": RESERVATION_ID,
            "concertId": CONCERT_ID,
            "seatId": SEAT_ID,
            "ticketId": TICKET_ID,
        }
    )
