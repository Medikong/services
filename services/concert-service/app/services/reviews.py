from app import entities as model
from app import schemas
from app.exceptions import ConflictError
from app.services.base import ConcertDomainService, now_utc
from app.services.serializers import draft_response, open_request_response, page, sale_policy_response


class ConcertReviewService(ConcertDomainService):
    def list_review_requests(self, limit: int) -> schemas.ConcertReviewRequestListResponse:
        return schemas.ConcertReviewRequestListResponse(
            items=[self._review_response(item) for item in self.reviews.list_review_requests(limit)],
            page=page(),
        )

    def get_review_request(self, request_id: str) -> schemas.ConcertReviewRequestResponse:
        return self._review_response(self._review_request(request_id))

    def approve_review_request(self, request_id: str) -> schemas.ConcertReviewRequestResponse:
        request = self._review_request(request_id)
        if request.status != "pending":
            raise ConflictError("review_request.invalid_state", "Review request is already closed.")
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
        return self._review_response(request)

    def reject_review_request(self, request_id: str, command: schemas.RejectCommand) -> schemas.ConcertReviewRequestResponse:
        request = self._review_request(request_id)
        if request.status != "pending":
            raise ConflictError("review_request.invalid_state", "Review request is already closed.")
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
        return self._review_response(request)

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
