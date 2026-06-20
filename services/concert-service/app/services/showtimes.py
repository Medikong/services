from datetime import UTC, date, datetime, timedelta

from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import ShowtimeEmptyUpdateError
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id
from app.services.catalog import SERVICE_TIMEZONE
from app.services.serializers import date_performance_response, page, performance_response, showtime_response


concert_metrics = ConcertTelemetryRecorder()
PERFORMANCE_LIST_MAX_LIMIT = 24


class ShowtimeService(ConcertDomainService):
    def create_showtime(self, concert_id: str, request: schemas.ShowtimeCreateRequest) -> schemas.ShowtimeResponse:
        """회차 생성 command 결과를 metric으로 남긴다."""
        try:
            self._concert(concert_id)
            self._venue(request.venueId)
            showtime = model.Showtime(
                id=new_id(),
                concert_id=concert_id,
                venue_id=request.venueId,
                starts_at=request.startsAt,
                ends_at=request.endsAt,
                status="draft",
            )
            self.add(showtime)
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_SHOWTIME, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_SHOWTIME, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_SHOWTIME, result=MetricResult.SUCCESS))
        return showtime_response(showtime)

    def update_showtime(self, showtime_id: str, request: schemas.ShowtimeUpdateRequest) -> schemas.ShowtimeResponse:
        """회차 수정 command 결과를 metric으로 남긴다."""
        try:
            showtime = self._showtime(showtime_id)
            values = request.model_dump(exclude_unset=True)
            if not values:
                raise ShowtimeEmptyUpdateError()
            if "startsAt" in values:
                showtime.starts_at = values["startsAt"]
            if "endsAt" in values:
                showtime.ends_at = values["endsAt"]
            if "status" in values:
                showtime.status = values["status"]
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SHOWTIME, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SHOWTIME, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SHOWTIME, result=MetricResult.SUCCESS))
        return showtime_response(showtime)

    def list_performances(self, concert_id: str, limit: int) -> schemas.PerformanceListResponse:
        """회차 목록 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.PERFORMANCES)
        try:
            self._concert(concert_id)
            response = schemas.PerformanceListResponse(
                items=[performance_response(item) for item in self.showtimes.list_showtimes(concert_id, min(limit, PERFORMANCE_LIST_MAX_LIMIT))],
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

    def list_performances_by_date(self, concert_id: str, selected_date: date) -> schemas.DatePerformanceListResponse:
        """선택 날짜의 performance만 반환한다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.PERFORMANCES)
        try:
            self._ensure_concert_exists(concert_id)
            day_start = datetime(selected_date.year, selected_date.month, selected_date.day, tzinfo=UTC)
            day_end = day_start + timedelta(days=1)
            response = schemas.DatePerformanceListResponse(
                concertId=concert_id,
                date=selected_date,
                timezone=SERVICE_TIMEZONE,
                performances=[
                    date_performance_response(item)
                    for item in self.showtimes.list_showtimes_between(concert_id, day_start, day_end)
                ],
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
