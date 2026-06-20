from datetime import timedelta

from sqlalchemy.orm import Session

from observability import DOMAIN_REJECTION_OBSERVATION, HttpError, TraceRecorder, trace_recorder

from app import entities as model
from app import schemas
from app.exceptions import (
    ReservationCancelInvalidStateError,
    ReservationExpireInvalidStateError,
    SalesNotOpenError,
    SeatAlreadyReservedError,
)
from app.metrics.labels import ReservationCommand
from app.metrics.recorder import ReservationTelemetryRecorder
from app.services.base import ACTIVE_STATUSES, ReservationDomainService, new_id, now_utc
from app.services.serializers import active_seat_key, reservation_response


class ReservationCommandService(ReservationDomainService):
    def __init__(
        self,
        db: Session,
        trace: TraceRecorder | None = None,
        telemetry: ReservationTelemetryRecorder | None = None,
    ) -> None:
        """예약 command 실행에 필요한 저장소와 관측성 의존성을 보관한다."""
        super().__init__(db)
        self.trace = trace or trace_recorder()
        self.telemetry = telemetry or ReservationTelemetryRecorder()

    def create_reservation(self, user_id: str, request: schemas.CreateReservationRequest) -> schemas.ReservationResponse:
        """예약을 생성하고 좌석 충돌/거절/실패 metric을 남긴다."""
        attempt = self.telemetry.start_command(ReservationCommand.CREATE)
        trace = self.trace
        concert_id = concert_id_from_request(request)
        trace.attribute("app.use_case", "reserve_seat")
        trace.attribute("concert.id", concert_id)
        trace.attribute("performance.id", request.performanceId)
        trace.attribute("seat.id", request.seatId)

        try:
            with trace.span("reservation.reserve_seat"):
                sales_state = self.sales.get_sales_state(concert_id)
                if sales_state is not None and sales_state.sales_status in {"paused", "closed"}:
                    raise SalesNotOpenError()
                if self.reservations.find_active_reservation(request.performanceId, request.seatId) is not None:
                    raise SeatAlreadyReservedError(request.seatId)
                created_at = now_utc()
                reservation = model.Reservation(
                    id=new_id(),
                    user_id=user_id,
                    concert_id=concert_id,
                    showtime_id=request.showtimeId or request.performanceId,
                    performance_id=request.performanceId,
                    seat_id=request.seatId,
                    status="pending",
                    active_seat_key=active_seat_key(request.performanceId, request.seatId),
                    expires_at=created_at + timedelta(minutes=5),
                    created_at=created_at,
                )
                self.add(reservation)
                self._commit_or_reservation_conflict(request.seatId)
                trace.attribute("reservation.id", reservation.id)
                trace.event("seat.hold.created", {"reservation.id": reservation.id, "seat.id": reservation.seat_id})

            attempt.mark_success()
            return reservation_response(reservation)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_error_code(exc.code)
            raise
        finally:
            attempt.record()

    def cancel_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        """예약을 취소하고 command 결과 metric을 남긴다."""
        attempt = self.telemetry.start_command(ReservationCommand.CANCEL)
        try:
            reservation = self._reservation(reservation_id)
            if reservation.status not in ACTIVE_STATUSES:
                raise ReservationCancelInvalidStateError()
            reservation.status = "canceled"
            reservation.active_seat_key = None
            reservation.updated_at = now_utc()
            self.commit()
            attempt.mark_success()
            return reservation_response(reservation)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_error_code(exc.code)
            raise
        finally:
            attempt.record()

    def confirm_reservation(self, reservation_id: str) -> None:
        reservation = self._reservation(reservation_id)
        if reservation.status not in ACTIVE_STATUSES:
            return
        reservation.status = "TICKETED"
        reservation.updated_at = now_utc()
        self.commit()

    def expire_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        """예약을 만료시키고 command 결과 metric을 남긴다."""
        attempt = self.telemetry.start_command(ReservationCommand.EXPIRE)
        try:
            reservation = self._reservation(reservation_id)
            if reservation.status != "pending":
                raise ReservationExpireInvalidStateError()
            reservation.status = "expired"
            reservation.active_seat_key = None
            reservation.updated_at = now_utc()
            self.commit()
            attempt.mark_success()
            return reservation_response(reservation)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_error_code(exc.code)
            raise
        finally:
            attempt.record()


class ReservationQueryService(ReservationDomainService):
    def list_my_reservations(self, user_id: str, limit: int) -> schemas.ReservationListResponse:
        return schemas.ReservationListResponse(
            items=[reservation_response(item) for item in self.reservations.list_user_reservations(user_id, limit)],
            page=schemas.PageInfo(hasNext=False),
        )

    def get_reservation(self, reservation_id: str) -> schemas.ReservationResponse:
        return reservation_response(self._reservation(reservation_id))


def concert_id_from_request(request: schemas.CreateReservationRequest) -> str:
    return request.concertId
