from metrics import MetricResult

from app import entities as model
from app import schemas
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id
from app.services.serializers import page, venue_response


concert_metrics = ConcertTelemetryRecorder()


class VenueService(ConcertDomainService):
    def create_venue(self, request: schemas.VenueCreateRequest) -> schemas.VenueResponse:
        """공연장 생성 command 결과를 metric으로 남긴다."""
        try:
            venue = model.Venue(id=new_id(), name=request.name, address=request.address, total_seats=request.totalSeats)
            self.add(venue)
            self.commit()
        except Exception:
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_VENUE, result=MetricResult.FAILURE)
            )
            raise
        concert_metrics.record(
            ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_VENUE, result=MetricResult.SUCCESS)
        )
        return venue_response(venue)

    def list_venues(self, limit: int) -> schemas.VenueListResponse:
        """공연장 목록 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.VENUES)
        try:
            response = schemas.VenueListResponse(items=[venue_response(item) for item in self.venues.list_venues(limit)], page=page())
            attempt.mark_success()
            return response
        finally:
            attempt.record()
