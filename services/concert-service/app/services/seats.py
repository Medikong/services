from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import (
    SeatGradeAlreadyExistsError,
    SeatMapContainsDuplicateSeatsError,
    SeatNotFoundError,
)
from app.metrics.events import SeatInventoryCommandRecorded
from app.metrics.labels import CatalogResource, SeatInventoryCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import hold_request_response, page, seat_grade_response, seat_response


concert_metrics = ConcertTelemetryRecorder()


class SeatService(ConcertDomainService):
    def list_seats(self, showtime_id: str, limit: int) -> schemas.SeatListResponse:
        """좌석 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.SEATS)
        try:
            self._showtime(showtime_id)
            response = schemas.SeatListResponse(items=[seat_response(item) for item in self.seats.list_seats(showtime_id, limit)], page=page())
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def upload_seat_map(self, showtime_id: str, request: schemas.SeatMapRequest) -> None:
        """좌석 맵 업로드 command 결과를 metric으로 남긴다."""
        try:
            self._showtime(showtime_id)
            self.seats.delete_showtime_seats(showtime_id)
            for section in request.sections:
                for row in section.rows:
                    for number in row.seatNumbers:
                        self.add(
                            model.Seat(
                                id=f"seat-{showtime_id}-{section.name}-{row.name}-{number}".replace(" ", "-"),
                                showtime_id=showtime_id,
                                section=section.name,
                                row_label=row.name,
                                number=number,
                                status="sellable",
                            )
                        )
            self._commit_or_domain_rejection(SeatMapContainsDuplicateSeatsError)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPLOAD_SEAT_MAP, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPLOAD_SEAT_MAP, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPLOAD_SEAT_MAP, result=MetricResult.SUCCESS))

    def update_seat_inventory(self, showtime_id: str, request: schemas.SeatInventoryUpdateRequest) -> None:
        """좌석 재고 수정 command 결과를 metric으로 남긴다."""
        try:
            self._showtime(showtime_id)
            for item in request.seats:
                seat = self.seats.get_seat(item.seatId)
                if seat is None or seat.showtime_id != showtime_id:
                    raise SeatNotFoundError(item.seatId)
                seat.status = item.status
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPDATE_SEAT_INVENTORY, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPDATE_SEAT_INVENTORY, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.UPDATE_SEAT_INVENTORY, result=MetricResult.SUCCESS))

    def create_seat_grades(self, showtime_id: str, request: schemas.SeatGradeCreateRequest) -> schemas.SeatGradeListResponse:
        """좌석 등급 생성 command 결과를 metric으로 남긴다."""
        try:
            self._showtime(showtime_id)
            items: list[model.SeatGrade] = []
            for grade in request.grades:
                entity = model.SeatGrade(
                    id=grade.id,
                    showtime_id=showtime_id,
                    name=grade.name,
                    price=grade.price,
                    color=grade.color,
                )
                self.add(entity)
                items.append(entity)
            self._commit_or_domain_rejection(SeatGradeAlreadyExistsError)
            response = schemas.SeatGradeListResponse(items=[seat_grade_response(item) for item in items])
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_SEAT_GRADES, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_SEAT_GRADES, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_SEAT_GRADES, result=MetricResult.SUCCESS))
        return response

    def create_hold_request(self, showtime_id: str, request: schemas.HoldSeatRequestCreateRequest) -> schemas.HoldSeatRequestResponse:
        """좌석 hold 요청 command 결과를 metric으로 남긴다."""
        try:
            self._showtime(showtime_id)
            for seat_id in request.seatIds:
                seat = self.seats.get_seat(seat_id)
                if seat is None or seat.showtime_id != showtime_id:
                    raise SeatNotFoundError(seat_id)
            hold = model.HoldSeatRequest(
                id=new_id("hold"),
                showtime_id=showtime_id,
                type=request.type,
                seat_ids=request.seatIds,
                reason=request.reason,
                status="requested",
            )
            self.add(hold)
            self.commit()
            response = hold_request_response(hold)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_HOLD_REQUEST, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_HOLD_REQUEST, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(SeatInventoryCommandRecorded(command=SeatInventoryCommand.CREATE_HOLD_REQUEST, result=MetricResult.SUCCESS))
        return response
