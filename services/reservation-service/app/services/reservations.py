from datetime import timedelta

from app import entities as model
from app import schemas
from app.exceptions import ConflictError
from app.services.base import ACTIVE_STATUSES, ReservationDomainService, new_id, now_utc
from app.services.serializers import active_seat_key, reservation_response


class ReservationCommandService(ReservationDomainService):
    def create_reservation(self, user_id: str, request: schemas.CreateReservationRequest) -> schemas.ReservationResponse:
        sales_state = self.sales.get_sales_state(concert_id_from_request(request))
        if sales_state is not None and sales_state.sales_status in {"paused", "closed"}:
            raise ConflictError("sales.not_open", "Sales are not open for this concert.")
        if self.reservations.find_active_reservation(request.performanceId, request.seatId) is not None:
            raise ConflictError("reservation.conflict", "Seat is already reserved.", {"seatId": request.seatId})
        created_at = now_utc()
        reservation = model.Reservation(
            id=new_id("rsv"),
            user_id=user_id,
            concert_id=concert_id_from_request(request),
            showtime_id=request.showtimeId or request.performanceId,
            performance_id=request.performanceId,
            seat_id=request.seatId,
            status="pending",
            active_seat_key=active_seat_key(request.performanceId, request.seatId),
            expires_at=created_at + timedelta(minutes=5),
            created_at=created_at,
        )
        self.add(reservation)
        self._commit_or_reservation_conflict()
        return reservation_response(reservation)

    def cancel_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        reservation = self._reservation(reservation_id)
        if reservation.status not in ACTIVE_STATUSES:
            raise ConflictError("reservation.invalid_state", "Only active reservations can be canceled.")
        reservation.status = "canceled"
        reservation.active_seat_key = None
        reservation.updated_at = now_utc()
        self.commit()
        return reservation_response(reservation)

    def expire_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        reservation = self._reservation(reservation_id)
        if reservation.status != "pending":
            raise ConflictError("reservation.invalid_state", "Only pending reservations can be expired.")
        reservation.status = "expired"
        reservation.active_seat_key = None
        reservation.updated_at = now_utc()
        self.commit()
        return reservation_response(reservation)


class ReservationQueryService(ReservationDomainService):
    def list_my_reservations(self, user_id: str, limit: int) -> schemas.ReservationListResponse:
        return schemas.ReservationListResponse(
            items=[reservation_response(item) for item in self.reservations.list_user_reservations(user_id, limit)],
            page=schemas.PageInfo(hasNext=False),
        )

    def get_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        return reservation_response(self._reservation(reservation_id))


def concert_id_from_request(request: schemas.CreateReservationRequest) -> str:
    return request.concertId or f"concert-{request.performanceId}"
