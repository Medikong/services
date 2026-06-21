from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import (
    SeatGradeAlreadyExistsError,
    SeatMapContainsDuplicateSeatsError,
    SeatNotFoundError,
    ShowtimeNotFoundError,
)
from app.metrics.events import SeatInventoryCommandRecorded
from app.metrics.labels import CatalogResource, SeatInventoryCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import hold_request_response, page, seat_grade_response, seat_map_status, seat_response


concert_metrics = ConcertTelemetryRecorder()
SEAT_LIST_MAX_LIMIT = 500
SEAT_MAP_DEFAULT_LIMIT = 200
SEAT_MAP_MAX_LIMIT = 500


class SeatService(ConcertDomainService):
    def list_seats(self, showtime_id: str, limit: int) -> schemas.SeatListResponse:
        """좌석 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.SEATS)
        try:
            self._showtime(showtime_id)
            response = schemas.SeatListResponse(
                items=[seat_response(item) for item in self.seats.list_seats(showtime_id, min(limit, SEAT_LIST_MAX_LIMIT))],
                page=page(),
            )
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def get_seat_map(
        self,
        showtime_id: str,
        limit: int = SEAT_MAP_DEFAULT_LIMIT,
        offset: int = 0,
        section_id: str | None = None,
    ) -> schemas.SeatMapResponse:
        """선택한 performance의 좌석도와 현재 좌석 상태를 반환한다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.SEATS)
        try:
            response = self._seat_map_response(showtime_id, min(limit, SEAT_MAP_MAX_LIMIT), offset, section_id)
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def _seat_map_response(self, showtime_id: str, limit: int, offset: int, section_id: str | None) -> schemas.SeatMapResponse:
        showtime = self.showtimes.get_seat_map_showtime(showtime_id)
        if showtime is None:
            raise ShowtimeNotFoundError(showtime_id)

        grades = self.showtimes.list_seat_map_grades(showtime_id)
        grades_by_name = {grade.name: grade for grade in grades}
        fallback_grade = grades[0] if grades else None
        section_rows = self.showtimes.list_seat_map_sections(showtime_id)
        selected_total = 0
        sections: list[schemas.SeatMapSectionResponse] = []
        for section in section_rows:
            if section_id is None or section.section == section_id:
                selected_total += section.total_count
            grade = grades_by_name.get(section.section, fallback_grade)
            grade_code = grade.name if grade is not None else "GENERAL"
            price = grade.price if grade is not None else 0
            sections.append(
                schemas.SeatMapSectionResponse(
                    sectionId=section.section,
                    name=section.section,
                    gradeCode=grade_code,
                    price=price,
                    currency="KRW",
                    available=section.available_count > 0,
                    availableCount=section.available_count,
                    totalCount=section.total_count,
                )
            )

        seat_rows = self.showtimes.list_seat_map_seats(showtime_id, limit, offset, section_id)
        seats = []
        for seat in seat_rows:
            grade = grades_by_name.get(seat.section, fallback_grade)
            seats.append(
                schemas.SeatMapSeatResponse(
                    seatId=seat.id,
                    sectionId=seat.section,
                    row=seat.row_label,
                    number=seat.number,
                    gradeCode=grade.name if grade is not None else "GENERAL",
                    status=seat_map_status(seat.status),
                )
            )

        return schemas.SeatMapResponse(
            performanceId=showtime.id,
            venue=schemas.SeatMapVenueResponse(venueId=showtime.venue_id, name=showtime.venue_name),
            mapVersion=showtime.starts_at.isoformat(),
            sections=sections,
            seats=seats,
            seatLimit=limit,
            seatOffset=offset,
            hasMoreSeats=offset + len(seats) < selected_total,
        )

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
                                id=new_id(),
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
                    id=new_id(),
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
                id=new_id(),
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
