from app import entities as model
from app import schemas
from app.exceptions import NotFoundError
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import hold_request_response, page, seat_grade_response, seat_response


class SeatService(ConcertDomainService):
    def list_seats(self, showtime_id: str, limit: int) -> schemas.SeatListResponse:
        self._showtime(showtime_id)
        return schemas.SeatListResponse(items=[seat_response(item) for item in self.seats.list_seats(showtime_id, limit)], page=page())

    def upload_seat_map(self, showtime_id: str, request: schemas.SeatMapRequest) -> None:
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
        self._commit_or_conflict("seat_map.conflict", "Seat map contains duplicate seats.")

    def update_seat_inventory(self, showtime_id: str, request: schemas.SeatInventoryUpdateRequest) -> None:
        self._showtime(showtime_id)
        for item in request.seats:
            seat = self.seats.get_seat(item.seatId)
            if seat is None or seat.showtime_id != showtime_id:
                raise NotFoundError("seat", item.seatId)
            seat.status = item.status
        self.commit()

    def create_seat_grades(self, showtime_id: str, request: schemas.SeatGradeCreateRequest) -> schemas.SeatGradeListResponse:
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
        self._commit_or_conflict("seat_grade.conflict", "Seat grade already exists.")
        return schemas.SeatGradeListResponse(items=[seat_grade_response(item) for item in items])

    def create_hold_request(self, showtime_id: str, request: schemas.HoldSeatRequestCreateRequest) -> schemas.HoldSeatRequestResponse:
        self._showtime(showtime_id)
        for seat_id in request.seatIds:
            seat = self.seats.get_seat(seat_id)
            if seat is None or seat.showtime_id != showtime_id:
                raise NotFoundError("seat", seat_id)
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
        return hold_request_response(hold)
