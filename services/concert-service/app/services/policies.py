from app import entities as model
from app import schemas
from app.exceptions import ConflictError
from app.services.base import ConcertDomainService, new_id, now_utc
from app.services.serializers import open_request_response, sale_policy_response


class SalePolicyService(ConcertDomainService):
    def update_sale_policy(self, concert_id: str, request: schemas.SalePolicyUpdateRequest) -> schemas.SalePolicyResponse:
        self._concert(concert_id)
        policy = self.sale_policies.get_sale_policy(concert_id)
        if policy is None:
            policy = model.SalePolicy(concert_id=concert_id, max_tickets_per_user=request.maxTicketsPerUser, refund_policy=request.refundPolicy)
            self.add(policy)
        policy.presale_enabled = request.presaleEnabled
        policy.fanclub_verification_required = request.fanclubVerificationRequired
        policy.max_tickets_per_user = request.maxTicketsPerUser
        policy.refund_policy = request.refundPolicy
        policy.status = "submitted"
        concert = self._concert(concert_id)
        self.add(
            model.ConcertReviewRequest(
                id=new_id("review"),
                concert_id=concert_id,
                provider_id=concert.provider_id,
                type="sale_policy",
                status="pending",
                submitted_at=now_utc(),
            )
        )
        self.commit()
        return sale_policy_response(policy)

    def get_sale_policy(self, concert_id: str) -> schemas.SalePolicyResponse:
        return sale_policy_response(self._sale_policy(concert_id))

    def approve_sale_policy(self, concert_id: str) -> schemas.SalePolicyResponse:
        policy = self._sale_policy(concert_id)
        if policy.status == "approved":
            raise ConflictError("sale_policy.invalid_state", "Sale policy is already approved.")
        policy.status = "approved"
        self.commit()
        return sale_policy_response(policy)

    def reject_sale_policy(self, concert_id: str, command: schemas.RejectCommand) -> schemas.SalePolicyResponse:
        policy = self._sale_policy(concert_id)
        if policy.status == "rejected":
            raise ConflictError("sale_policy.invalid_state", "Sale policy is already rejected.")
        policy.status = "rejected"
        concert = self._concert(concert_id)
        concert.review_reason = command.reason
        concert.last_reviewed_at = now_utc()
        self.commit()
        return sale_policy_response(policy)


class OpenPolicyService(ConcertDomainService):
    def submit_open_request(self, concert_id: str, request: schemas.OpenRequestCreateRequest) -> schemas.OpenRequestResponse:
        concert = self._concert(concert_id)
        open_request = model.OpenRequest(
            id=new_id("openreq"),
            concert_id=concert_id,
            requested_open_at=request.requestedOpenAt,
            message=request.message,
            status="requested",
        )
        self.add(open_request)
        self.add(
            model.ConcertReviewRequest(
                id=new_id("review"),
                concert_id=concert_id,
                provider_id=concert.provider_id,
                type="open_request",
                status="pending",
                submitted_at=now_utc(),
            )
        )
        self.commit()
        return open_request_response(open_request)

    def update_open_schedule(self, concert_id: str, request: schemas.OpenScheduleUpdateRequest) -> schemas.OpenScheduleResponse:
        concert = self._concert(concert_id)
        concert.opens_at = request.opensAt
        concert.open_schedule_status = "scheduled"
        concert.status = "scheduled"
        self.commit()
        return schemas.OpenScheduleResponse(concertId=concert.id, opensAt=concert.opens_at, status="scheduled")

    def set_reopen_policy(self, concert_id: str, request: schemas.CanceledSeatReopenPolicyRequest) -> schemas.CanceledSeatReopenPolicyResponse:
        self._concert(concert_id)
        policy = model.CanceledSeatReopenPolicy(
            concert_id=concert_id,
            enabled=request.enabled,
            reopen_delay_seconds=request.reopenDelaySeconds,
            batch_size=request.batchSize,
            comment=request.comment,
        )
        self.db.merge(policy)
        self.commit()
        return schemas.CanceledSeatReopenPolicyResponse(
            concertId=concert_id,
            enabled=request.enabled,
            reopenDelaySeconds=request.reopenDelaySeconds,
            batchSize=request.batchSize,
        )


class ReviewStatusService(ConcertDomainService):
    def review_status(self, concert_id: str) -> schemas.ReviewStatusResponse:
        concert = self._concert(concert_id)
        policy = self.sale_policies.get_sale_policy(concert_id)
        open_request = self.open_policies.latest_open_request(concert_id)
        return schemas.ReviewStatusResponse(
            concertId=concert.id,
            concertStatus=concert.status,
            salePolicyStatus=policy.status if policy else "draft",
            openRequestStatus=open_request.status if open_request else "none",
            lastReviewedAt=concert.last_reviewed_at,
            reason=concert.review_reason,
        )
