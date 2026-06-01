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


def test_event_topics_are_stable() -> None:
    assert RESERVATION_CREATED_TOPIC == "reservation-created"
    assert RESERVATION_EXPIRED_TOPIC == "reservation-expired"
    assert PAYMENT_APPROVED_TOPIC == "payment-approved"
    assert PAYMENT_FAILED_TOPIC == "payment-failed"
    assert TICKET_ISSUED_TOPIC == "ticket-issued"


def test_payment_approved_event_matches_ticket_issue_input() -> None:
    event = PaymentApprovedEvent.model_validate(
        {
            "eventId": "event-payment-1",
            "eventType": "payment-approved",
            "userId": "1",
            "sourceId": "payment-1",
            "reservationId": "reservation-1",
            "concertId": "concert-1",
            "performanceId": "showtime-1",
            "seatId": "seat-A1",
            "paymentId": "payment-1",
            "amount": 50000,
            "occurredAt": "2026-05-13T10:00:00Z",
            "producer": "payment-service",
            "correlationId": "corr-1",
        }
    )

    assert event.userId == "1"
    assert event.reservationId == "reservation-1"
    assert event.concertId == "concert-1"
    assert event.seatId == "seat-A1"


def test_all_reservation_flow_events_accept_minimum_payloads() -> None:
    common = {
        "eventId": "event-1",
        "userId": "1",
        "sourceId": "source-1",
        "occurredAt": "2026-05-13T10:00:00Z",
        "producer": "contract-test",
    }

    ReservationCreatedEvent.model_validate(
        common
        | {
            "eventType": "reservation-created",
            "reservationId": "reservation-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
        }
    )
    ReservationExpiredEvent.model_validate(
        common
        | {
            "eventType": "reservation-expired",
            "reservationId": "reservation-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
        }
    )
    PaymentFailedEvent.model_validate(
        common
        | {
            "eventType": "payment-failed",
            "reservationId": "reservation-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
            "paymentId": "payment-1",
            "amount": 50000,
        }
    )
    TicketIssuedEvent.model_validate(
        common
        | {
            "eventType": "ticket-issued",
            "reservationId": "reservation-1",
            "concertId": "concert-1",
            "seatId": "seat-A1",
            "ticketId": "ticket-1",
        }
    )
