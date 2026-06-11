from fastapi import FastAPI, status
from observability import DOMAIN_REJECTION_OBSERVATION, ErrorObservation, HttpError, register_error_handlers

from app.config import settings


class ConcertNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, concert_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "concert.not_found",
            "concert not found.",
            {"id": concert_id},
            domain="concert",
        )


class PublicConcertNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, concert_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "concert.not_found",
            "concert not found.",
            {"id": concert_id},
            domain="concert",
        )


class VenueNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, venue_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "venue.not_found",
            "venue not found.",
            {"id": venue_id},
            domain="concert",
        )


class ShowtimeNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, showtime_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "showtime.not_found",
            "showtime not found.",
            {"id": showtime_id},
            domain="concert",
        )


class SalePolicyNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, concert_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "sale_policy.not_found",
            "sale_policy not found.",
            {"id": concert_id},
            domain="concert",
        )


class ReviewRequestNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, request_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "review_request.not_found",
            "review_request not found.",
            {"id": request_id},
            domain="concert",
        )


class SeatNotFoundError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self, seat_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            "seat.not_found",
            "seat not found.",
            {"id": seat_id},
            domain="concert",
        )


class ConcertEmptyUpdateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "concert.empty_update",
            "At least one field must be supplied.",
            domain="concert",
        )


class ShowtimeEmptyUpdateError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "showtime.empty_update",
            "At least one field must be supplied.",
            domain="concert",
        )


class SalePolicyAlreadyApprovedError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sale_policy.invalid_state",
            "Sale policy is already approved.",
            domain="concert",
        )


class SalePolicyAlreadyRejectedError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "sale_policy.invalid_state",
            "Sale policy is already rejected.",
            domain="concert",
        )


class ReviewRequestAlreadyClosedError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "review_request.invalid_state",
            "Review request is already closed.",
            domain="concert",
        )


class SeatGradeAlreadyExistsError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "seat_grade.conflict",
            "Seat grade already exists.",
            domain="concert",
        )


class SeatMapContainsDuplicateSeatsError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(
            status.HTTP_409_CONFLICT,
            "seat_map.conflict",
            "Seat map contains duplicate seats.",
            domain="concert",
        )


def register_exception_handlers(app: FastAPI) -> None:
    register_error_handlers(app, service_name=settings.service_name, domain="concert")
