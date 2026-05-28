from app import entities as model
from app import schemas


def active_seat_key(performance_id: str, seat_id: str) -> str:
    return f"{performance_id}:{seat_id}"


def reservation_response(reservation: model.Reservation) -> schemas.ReservationResponse:
    return schemas.ReservationResponse(
        id=reservation.id,
        userId=reservation.user_id,
        performanceId=reservation.performance_id,
        seatId=reservation.seat_id,
        status=reservation.status,
        expiresAt=reservation.expires_at,
        createdAt=reservation.created_at,
    )


def sales_command_response(state: model.SalesState) -> schemas.SalesCommandResponse:
    return schemas.SalesCommandResponse(
        concertId=state.concert_id,
        salesStatus=state.sales_status,
        changedAt=state.updated_at,
    )
