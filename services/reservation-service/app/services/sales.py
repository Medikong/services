from sqlalchemy.orm import Session

from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import schemas
from app.exceptions import (
    ClosedSalesCannotStartError,
    SalesAlreadyOpenError,
    SalesPauseInvalidStateError,
    SalesResumeInvalidStateError,
)
from app.metrics.events import SalesStateChangeRecorded
from app.metrics.labels import SalesStateAction
from app.metrics.recorder import ReservationTelemetryRecorder
from app.services.base import ReservationDomainService, now_utc
from app.services.serializers import sales_command_response


class SalesService(ReservationDomainService):
    def __init__(self, db: Session, telemetry: ReservationTelemetryRecorder | None = None) -> None:
        """판매 상태 use case 실행에 필요한 관측성 의존성을 보관한다."""
        super().__init__(db)
        self.telemetry = telemetry or ReservationTelemetryRecorder()

    def sales_summary(self, concert_id: str) -> schemas.SalesSummaryResponse:
        state = self.sales.get_or_create_sales_state(concert_id, now_utc())
        sold, reserved = self.sales.reservation_counts_for_concert(concert_id)
        self.commit()
        return schemas.SalesSummaryResponse(
            concertId=concert_id,
            salesStatus=state.sales_status,
            totalSeats=max(state.total_seats, sold + reserved),
            soldSeats=sold,
            reservedSeats=reserved,
            grossAmount=0,
            updatedAt=state.updated_at,
        )

    def showtime_sales_summary(self, showtime_id: str) -> schemas.ShowtimeSalesResponse:
        sold, reserved = self.sales.reservation_counts_for_showtime(showtime_id)
        total = sold + reserved
        return schemas.ShowtimeSalesResponse(
            showtimeId=showtime_id,
            totalSeats=total,
            availableSeats=0,
            soldSeats=sold,
            reservedSeats=reserved,
            grossAmount=0,
            updatedAt=now_utc(),
        )

    def start_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        """판매 시작 명령 결과 metric을 남긴다."""
        try:
            state = self.sales.get_or_create_sales_state(concert_id, now_utc())
            if state.sales_status == "open":
                raise SalesAlreadyOpenError()
            if state.sales_status == "closed":
                raise ClosedSalesCannotStartError()
            state.sales_status = "open"
            state.updated_at = now_utc()
            self.commit()
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.START, result=MetricResult.SUCCESS))
            return sales_command_response(state)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.START, result=MetricResult.REJECTION))
            raise

    def pause_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        """판매 일시정지 명령 결과 metric을 남긴다."""
        try:
            state = self._sales_state(concert_id)
            if state.sales_status != "open":
                raise SalesPauseInvalidStateError()
            state.sales_status = "paused"
            state.updated_at = now_utc()
            self.commit()
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.PAUSE, result=MetricResult.SUCCESS))
            return sales_command_response(state)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.PAUSE, result=MetricResult.REJECTION))
            raise

    def resume_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        """판매 재개 명령 결과 metric을 남긴다."""
        try:
            state = self._sales_state(concert_id)
            if state.sales_status != "paused":
                raise SalesResumeInvalidStateError()
            state.sales_status = "open"
            state.updated_at = now_utc()
            self.commit()
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.RESUME, result=MetricResult.SUCCESS))
            return sales_command_response(state)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            self.telemetry.record(SalesStateChangeRecorded(action=SalesStateAction.RESUME, result=MetricResult.REJECTION))
            raise
