from metrics import MetricResult
from observability import DOMAIN_REJECTION_OBSERVATION, HttpError

from app import entities as model
from app import schemas
from app.exceptions import (
    SalePolicyAlreadyApprovedError,
    SalePolicyAlreadyRejectedError,
)
from app.metrics.events import ConcertAdminCommandRecorded
from app.metrics.labels import CatalogResource, ConcertAdminCommand
from app.metrics.recorder import ConcertTelemetryRecorder
from app.services.base import ConcertDomainService, new_id, now_utc
from app.services.serializers import open_request_response, sale_policy_response


concert_metrics = ConcertTelemetryRecorder()


class SalePolicyService(ConcertDomainService):
    def update_sale_policy(self, concert_id: str, request: schemas.SalePolicyUpdateRequest) -> schemas.SalePolicyResponse:
        """판매 정책 수정 command 결과를 metric으로 남긴다."""
        try:
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
                    id=new_id(),
                    concert_id=concert_id,
                    provider_id=concert.provider_id,
                    type="sale_policy",
                    status="pending",
                    submitted_at=now_utc(),
                )
            )
            self.commit()
            response = sale_policy_response(policy)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SALE_POLICY, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SALE_POLICY, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_SALE_POLICY, result=MetricResult.SUCCESS))
        return response

    def get_sale_policy(self, concert_id: str) -> schemas.SalePolicyResponse:
        """판매 정책 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.SALE_POLICY)
        try:
            response = sale_policy_response(self._sale_policy(concert_id))
            attempt.mark_success()
            return response
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            attempt.mark_rejection()
            raise
        finally:
            attempt.record()

    def approve_sale_policy(self, concert_id: str) -> schemas.SalePolicyResponse:
        """판매 정책 승인 command 결과를 metric으로 남긴다."""
        try:
            policy = self._sale_policy(concert_id)
            if policy.status == "approved":
                raise SalePolicyAlreadyApprovedError()
            policy.status = "approved"
            self.commit()
            response = sale_policy_response(policy)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_SALE_POLICY, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_SALE_POLICY, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.APPROVE_SALE_POLICY, result=MetricResult.SUCCESS))
        return response

    def reject_sale_policy(self, concert_id: str, command: schemas.RejectCommand) -> schemas.SalePolicyResponse:
        """판매 정책 반려 command 결과를 metric으로 남긴다."""
        try:
            policy = self._sale_policy(concert_id)
            if policy.status == "rejected":
                raise SalePolicyAlreadyRejectedError()
            policy.status = "rejected"
            concert = self._concert(concert_id)
            concert.review_reason = command.reason
            concert.last_reviewed_at = now_utc()
            self.commit()
            response = sale_policy_response(policy)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_SALE_POLICY, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_SALE_POLICY, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.REJECT_SALE_POLICY, result=MetricResult.SUCCESS))
        return response


class OpenPolicyService(ConcertDomainService):
    def submit_open_request(self, concert_id: str, request: schemas.OpenRequestCreateRequest) -> schemas.OpenRequestResponse:
        """오픈 요청 제출 command 결과를 metric으로 남긴다."""
        try:
            concert = self._concert(concert_id)
            open_request = model.OpenRequest(
                id=new_id(),
                concert_id=concert_id,
                requested_open_at=request.requestedOpenAt,
                message=request.message,
                status="requested",
            )
            self.add(open_request)
            self.add(
                model.ConcertReviewRequest(
                    id=new_id(),
                    concert_id=concert_id,
                    provider_id=concert.provider_id,
                    type="open_request",
                    status="pending",
                    submitted_at=now_utc(),
                )
            )
            self.commit()
            response = open_request_response(open_request)
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SUBMIT_OPEN_REQUEST, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SUBMIT_OPEN_REQUEST, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SUBMIT_OPEN_REQUEST, result=MetricResult.SUCCESS))
        return response

    def update_open_schedule(self, concert_id: str, request: schemas.OpenScheduleUpdateRequest) -> schemas.OpenScheduleResponse:
        """오픈 일정 수정 command 결과를 metric으로 남긴다."""
        try:
            concert = self._concert(concert_id)
            concert.opens_at = request.opensAt
            concert.open_schedule_status = "scheduled"
            concert.status = "scheduled"
            self.commit()
            response = schemas.OpenScheduleResponse(concertId=concert.id, opensAt=concert.opens_at, status="scheduled")
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_OPEN_SCHEDULE, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_OPEN_SCHEDULE, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.UPDATE_OPEN_SCHEDULE, result=MetricResult.SUCCESS))
        return response

    def set_reopen_policy(self, concert_id: str, request: schemas.CanceledSeatReopenPolicyRequest) -> schemas.CanceledSeatReopenPolicyResponse:
        """취소 좌석 재오픈 정책 command 결과를 metric으로 남긴다."""
        try:
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
            response = schemas.CanceledSeatReopenPolicyResponse(
                concertId=concert_id,
                enabled=request.enabled,
                reopenDelaySeconds=request.reopenDelaySeconds,
                batchSize=request.batchSize,
            )
        except HttpError as exc:
            if exc.observation != DOMAIN_REJECTION_OBSERVATION:
                raise
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SET_REOPEN_POLICY, result=MetricResult.REJECTION))
            raise
        except Exception:
            concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SET_REOPEN_POLICY, result=MetricResult.FAILURE))
            raise
        concert_metrics.record(ConcertAdminCommandRecorded(command=ConcertAdminCommand.SET_REOPEN_POLICY, result=MetricResult.SUCCESS))
        return response


class ReviewStatusService(ConcertDomainService):
    def review_status(self, concert_id: str) -> schemas.ReviewStatusResponse:
        """심사 상태 조회 처리 시간과 결과를 metric으로 남긴다."""
        attempt = concert_metrics.start_catalog_query(CatalogResource.REVIEW_STATUS)
        try:
            concert = self._concert(concert_id)
            policy = self.sale_policies.get_sale_policy(concert_id)
            open_request = self.open_policies.latest_open_request(concert_id)
            response = schemas.ReviewStatusResponse(
                concertId=concert.id,
                concertStatus=concert.status,
                salePolicyStatus=policy.status if policy else "draft",
                openRequestStatus=open_request.status if open_request else "none",
                lastReviewedAt=concert.last_reviewed_at,
                reason=concert.review_reason,
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
