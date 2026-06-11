from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import ConcertEmptyUpdateError, PublicConcertNotFoundError
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id, now_utc
from app.services.serializers import draft_response, page, public_concert_response


concert_metrics = ConcertTelemetryRecorder()


class ConcertCatalogService(ConcertDomainService):
    def create_concert(self, provider_id: str, request: schemas.ConcertDraftCreateRequest) -> schemas.ConcertDraftResponse:
        """공연 생성 command 결과를 metric으로 남긴다."""
        try:
            created_at = now_utc()
            concert = model.Concert(
                id=new_id("concert"),
                provider_id=provider_id,
                title=request.title,
                description=request.description,
                poster_url=request.posterUrl,
                age_rating=request.ageRating,
                running_minutes=request.runningMinutes,
                status="draft",
                created_at=created_at,
            )
            self.add(concert)
            self.add(
                model.ConcertReviewRequest(
                    id=new_id("review"),
                    concert_id=concert.id,
                    provider_id=provider_id,
                    type="concert",
                    status="pending",
                    submitted_at=created_at,
                )
            )
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.REJECTION)
            )
            raise
        except Exception:
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.FAILURE)
            )
            raise
        concert_metrics.record(
            ConcertAdminCommandRecorded(command=ConcertAdminCommand.CREATE_CONCERT, result=MetricResult.SUCCESS)
        )
        return draft_response(concert)

    def update_concert(self, concert_id: str, request: schemas.ConcertUpdateRequest) -> schemas.ConcertDraftResponse:
        """공연 수정 command 결과를 metric으로 남긴다."""
        try:
            concert = self._concert(concert_id)
            values = request.model_dump(exclude_unset=True)
            if not values:
                raise ConcertEmptyUpdateError()
            if "title" in values:
                concert.title = values["title"]
            if "description" in values:
                concert.description = values["description"]
            if "posterUrl" in values:
                concert.poster_url = values["posterUrl"]
            if "ageRating" in values:
                concert.age_rating = values["ageRating"]
            if "runningMinutes" in values:
                concert.running_minutes = values["runningMinutes"]
            concert.updated_at = now_utc()
            self.commit()
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.REJECTION)
            )
            raise
        except Exception:
            concert_metrics.record(
                ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.FAILURE)
            )
            raise
        concert_metrics.record(
            ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_CONCERT, result=MetricResult.SUCCESS)
        )
        return draft_response(concert)

    def list_public_concerts(self, limit: int) -> schemas.ConcertListResponse:
        """공연 목록 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERTS)
        try:
            items = [public_concert_response(concert) for concert in self.concerts.list_concerts(limit) if concert.showtimes]
            response = schemas.ConcertListResponse(items=items, page=page())
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def get_public_concert(self, concert_id: str) -> schemas.ConcertResponse:
        """공연 상세 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.CONCERT)
        try:
            concert = self._concert(concert_id)
            if not concert.showtimes:
                raise PublicConcertNotFoundError(concert_id)
            response = public_concert_response(concert)
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()
