from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import ReviewRequestAlreadyClosedError
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, now_utc
from app.services.serializers import draft_response, open_request_response, page, sale_policy_response


concert_metrics = ConcertTelemetryRecorder()


class ConcertReviewService(ConcertDomainService):
    def list_review_requests(self, limit: int) -> schemas.ConcertReviewRequestListResponse:
        """심사 요청 목록 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.REVIEW_REQUESTS)
        try:
            response = schemas.ConcertReviewRequestListResponse(
                items=[self._review_response(item) for item in self.reviews.list_review_requests(limit)],
                page=page(),
            )
            attempt.mark_success()
            return response
        finally:
            attempt.record()

    def get_review_request(self, request_id: str) -> schemas.ConcertReviewRequestResponse:
        """심사 요청 단건 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.REVIEW_REQUESTS)
        try:
            response = self._review_response(self._review_request(request_id))
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def approve_review_request(self, request_id: str) -> schemas.ConcertReviewRequestResponse:
        """심사 승인 command 결과를 metric으로 남긴다."""
        try:
            request = self._review_request(request_id)
            if request.status != "pending":
                raise ReviewRequestAlreadyClosedError()
            request.status = "approved"
            request.reviewed_at = now_utc()
            concert = self._concert(request.concert_id)
            concert.last_reviewed_at = request.reviewed_at
            concert.review_reason = None
            if request.type == "concert":
                concert.status = "approved"
            elif request.type == "sale_policy" and concert.sale_policy:
                concert.sale_policy.status = "approved"
            elif request.type == "open_request":
                open_request = self.open_policies.latest_open_request(concert.id)
                if open_request:
                    open_request.status = "approved"
            self.commit()
            response = self._review_response(request)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_REVIEW_REQUEST, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_REVIEW_REQUEST, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_REVIEW_REQUEST, result=MetricResult.SUCCESS))
        return response

    def reject_review_request(self, request_id: str, command: schemas.RejectCommand) -> schemas.ConcertReviewRequestResponse:
        """심사 반려 command 결과를 metric으로 남긴다."""
        try:
            request = self._review_request(request_id)
            if request.status != "pending":
                raise ReviewRequestAlreadyClosedError()
            request.status = "rejected"
            request.reviewed_at = now_utc()
            request.reason = command.reason
            concert = self._concert(request.concert_id)
            concert.last_reviewed_at = request.reviewed_at
            concert.review_reason = command.reason
            if request.type == "concert":
                concert.status = "rejected"
            elif request.type == "sale_policy" and concert.sale_policy:
                concert.sale_policy.status = "rejected"
            elif request.type == "open_request":
                open_request = self.open_policies.latest_open_request(concert.id)
                if open_request:
                    open_request.status = "rejected"
            self.commit()
            response = self._review_response(request)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_REVIEW_REQUEST, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_REVIEW_REQUEST, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_REVIEW_REQUEST, result=MetricResult.SUCCESS))
        return response

    def _review_response(self, request: model.ConcertReviewRequest) -> schemas.ConcertReviewRequestResponse:
        concert = self._concert(request.concert_id)
        open_request = self.open_policies.latest_open_request(concert.id) if request.type == "open_request" else None
        return schemas.ConcertReviewRequestResponse(
            id=request.id,
            concertId=request.concert_id,
            providerId=request.provider_id,
            type=request.type,
            status=request.status,
            submittedAt=request.submitted_at,
            concert=draft_response(concert) if request.type == "concert" else None,
            salePolicy=sale_policy_response(concert.sale_policy) if request.type == "sale_policy" and concert.sale_policy else None,
            openRequest=open_request_response(open_request) if open_request else None,
        )
