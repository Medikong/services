from app import entities as model
from app import schemas
from app.exceptions import ConflictError
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import page, performance_response, showtime_response


class ShowtimeService(ConcertDomainService):
    def create_showtime(self, concert_id: str, request: schemas.ShowtimeCreateRequest) -> schemas.ShowtimeResponse:
        self._concert(concert_id)
        self._venue(request.venueId)
        showtime = model.Showtime(
            id=new_id("showtime"),
            concert_id=concert_id,
            venue_id=request.venueId,
            starts_at=request.startsAt,
            ends_at=request.endsAt,
            status="draft",
        )
        self.add(showtime)
        self.commit()
        return showtime_response(showtime)

    def update_showtime(self, showtime_id: str, request: schemas.ShowtimeUpdateRequest) -> schemas.ShowtimeResponse:
        showtime = self._showtime(showtime_id)
        values = request.model_dump(exclude_unset=True)
        if not values:
            raise ConflictError("showtime.empty_update", "At least one field must be supplied.")
        if "startsAt" in values:
            showtime.starts_at = values["startsAt"]
        if "endsAt" in values:
            showtime.ends_at = values["endsAt"]
        if "status" in values:
            showtime.status = values["status"]
        self.commit()
        return showtime_response(showtime)

    def list_performances(self, concert_id: str, limit: int) -> schemas.PerformanceListResponse:
        self._concert(concert_id)
        return schemas.PerformanceListResponse(
            items=[performance_response(item) for item in self.showtimes.list_showtimes(concert_id, limit)],
            page=page(),
        )
