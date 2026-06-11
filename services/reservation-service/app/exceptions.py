from fastapi import FastAPI, status
from observability import DOMAIN_REJECTION_OBSERVATION, ErrorObservation, HttpError, register_error_handlers

from app.config import settings


class ReservationNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, reservation_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "reservation.not_found",
            "reservation not found.",
            {"id": reservation_id},
            domain="reservation",
        )


class SalesStateNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, concert_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "sales.not_found",
            "sales not found.",
            {"id": concert_id},
            domain="reservation",
        )


class SalesNotOpenError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sales.not_open",
            "Sales are not open for this concert.",
            domain="reservation",
        )


class SeatAlreadyReservedError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, seat_id: str | None = None) -> None:
        details: dict[str, object] | None = {"seatId": seat_id} if seat_id is not None else None
        super().__init__(
            status.HTTP_409_CONFLICT,
            "reservation.conflict",
            "Seat is already reserved.",
            details,
            domain="reservation",
        )


class ReservationCancelInvalidStateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "reservation.invalid_state",
            "Only active reservations can be canceled.",
            domain="reservation",
        )


class ReservationExpireInvalidStateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "reservation.invalid_state",
            "Only pending reservations can be expired.",
            domain="reservation",
        )


class SalesAlreadyOpenError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sales.invalid_state",
            "Sales are already open.",
            domain="reservation",
        )


class ClosedSalesCannotStartError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sales.invalid_state",
            "Closed sales cannot be started.",
            domain="reservation",
        )


class SalesPauseInvalidStateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sales.invalid_state",
            "Only open sales can be paused.",
            domain="reservation",
        )


class SalesResumeInvalidStateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sales.invalid_state",
            "Only paused sales can be resumed.",
            domain="reservation",
        )


def register_exception_handlers(app: FastAPI) -> None:
    register_error_handlers(app, service_name=settings.service_name, domain="reservation")
