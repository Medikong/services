from dataclasses import dataclass
from datetime import UTC, datetime
from uuid import uuid4

from fastapi import HTTPException, status
from observability import TraceContext
from sqlalchemy import func
from sqlalchemy.orm import Session

from app.auth import UserContext
from app.metrics import PaymentEventType
from app.metrics.recorder import PaymentTelemetryRecorder
from app.models import Payment, PaymentEvent
from app.schemas import CreatePaymentRequest, SettlementBasisResponse
from app.services.payment_events import (
    PUBLISH_STATUS_PENDING,
    build_payment_event_draft,
    payment_event_type_for_status,
)


@dataclass(frozen=True)
class PaymentRequestContext:
    idempotency_key: str | None
    correlation_id: str | None
    trace_context: TraceContext | None = None


@dataclass(frozen=True)
class PaymentCreateResult:
    payment: Payment
    event_type: PaymentEventType | None


class PaymentService:
    def __init__(
        self,
        *,
        db: Session,
        telemetry: PaymentTelemetryRecorder,
    ) -> None:
        """결제 use case 실행에 필요한 저장소와 관측성 의존성을 보관한다."""
        self._db = db
        self._telemetry = telemetry

    async def create_payment(
        self,
        *,
        request_body: CreatePaymentRequest,
        user: UserContext,
        context: PaymentRequestContext,
    ) -> PaymentCreateResult:
        """결제를 생성하고 발행할 이벤트를 outbox에 저장한다."""
        # 결제 시도 시작 시점부터 결과와 처리 시간을 함께 기록한다.
        attempt = self._telemetry.start_payment(request_body.method)
        try:
            if context.idempotency_key:
                # 같은 사용자와 idempotency key 조합이면 기존 결제를 재사용한다.
                existing = (
                    self._db.query(Payment)
                    .filter(
                        Payment.user_id == user.user_id,
                        Payment.idempotency_key == context.idempotency_key,
                    )
                    .one_or_none()
                )
                if existing is not None:
                    attempt.mark_duplicate()
                    return PaymentCreateResult(payment=existing, event_type=None)

            # 시뮬레이션 입력을 실제 결제 상태와 metric 결과로 변환한다.
            payment_status = _status_from_simulation(request_body.simulation)
            attempt.mark_payment_status(payment_status)

            # 결제 레코드는 이벤트 payload 생성 전 DB 세션에 먼저 올린다.
            payment = Payment(
                id=str(uuid4()),
                reservation_id=request_body.reservationId,
                concert_id=request_body.concertId,
                user_id=user.user_id,
                amount=request_body.amount,
                method=request_body.method,
                status=payment_status,
                idempotency_key=context.idempotency_key,
                approved_at=datetime.now(UTC) if request_body.simulation == "approve" else None,
            )
            self._db.add(payment)

            event_type = payment_event_type_for_status(payment.status)
            if event_type is not None:
                # 승인/실패 결제만 dispatcher가 발행할 outbox 이벤트를 만든다.
                event_draft = build_payment_event_draft(
                    event_type=event_type,
                    payment=payment,
                    request_body=request_body,
                    user=user,
                    correlation_id=context.correlation_id,
                    trace_context=context.trace_context,
                )
                self._db.add(
                    PaymentEvent(
                        id=event_draft.event_id,
                        event_type=event_draft.event_type.value,
                        payment_id=payment.id,
                        payload=event_draft.payload,
                        trace_context=event_draft.trace_context,
                        publish_status=PUBLISH_STATUS_PENDING,
                    )
                )

            # Payment와 PaymentEvent(outbox)는 같은 DB 트랜잭션으로 묶는다.
            self._db.commit()
            self._db.refresh(payment)

            return PaymentCreateResult(payment=payment, event_type=event_type)
        except HTTPException as exc:
            if exc.status_code == status.HTTP_422_UNPROCESSABLE_ENTITY:
                # 잘못된 시뮬레이션 값은 비즈니스 거절로 분류한다.
                attempt.mark_invalid_simulation()
            raise
        finally:
            # 성공/실패/예외와 무관하게 결제 시도 metric은 한 번 남긴다.
            attempt.record()

    def get_payment(self, *, payment_id: str, user: UserContext) -> Payment:
        """사용자 권한을 확인한 뒤 단일 결제를 조회한다."""
        # 관리자가 아니면 본인 결제만 조회할 수 있다.
        payment = self._db.get(Payment, payment_id)
        if payment is None:
            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="Payment not found")
        if user.role != "ADMIN" and payment.user_id != user.user_id:
            raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
        return payment

    def settlement_for_concert(self, concert_id: str) -> SettlementBasisResponse:
        """공연별 승인 결제를 집계해 정산 기준 값을 만든다."""
        # 정산 기준은 승인된 결제만 집계한다.
        gross_amount, ticket_count = (
            self._db.query(
                func.coalesce(func.sum(Payment.amount), 0),
                func.count(),
            )
            .filter(Payment.concert_id == concert_id, Payment.status == "approved")
            .one()
        )
        gross = int(gross_amount or 0)
        count = int(ticket_count or 0)

        # 현재 환불은 별도 흐름이 없어 0으로 두고 플랫폼 수수료만 계산한다.
        refund = 0
        platform_fee = int(gross * 0.1)
        net = gross - refund
        return SettlementBasisResponse(
            concertId=concert_id,
            grossAmount=gross,
            refundAmount=refund,
            netAmount=net,
            ticketCount=count,
            platformFeeAmount=platform_fee,
            providerSettlementAmount=net - platform_fee,
            calculatedAt=datetime.now(UTC),
        )


def _status_from_simulation(simulation: str) -> str:
    """테스트 시뮬레이션 값을 결제 도메인 상태로 변환한다."""
    # 테스트용 결제 시뮬레이션 값을 도메인 상태로 변환한다.
    if simulation == "approve":
        return "approved"
    if simulation == "fail":
        return "failed"
    if simulation == "delay":
        return "delayed"
    raise HTTPException(status_code=status.HTTP_422_UNPROCESSABLE_ENTITY, detail="Invalid payment simulation")
