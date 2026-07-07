import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never

from fastapi import Depends, FastAPI, Header, HTTPException, status
from fastapi.responses import ORJSONResponse, PlainTextResponse

from app.db import AppResources, lifespan_for, resources_from_env
from app.messaging import (
    NoopPaymentEventPublisher,
    PaymentEventPublisher,
    PaymentEventPublisherRef,
)
from app.metrics import PaymentMetrics
from app.models import (
    ApprovePaymentRequest,
    FailPaymentRequest,
    IdempotencyKey,
    OrderId,
    HealthResponse,
    PaymentId,
    PaymentResponse,
    ReadinessResponse,
    UserId,
    UserRole,
)
from app.repository import PaymentRepository
from app.store import (
    ApprovePaymentCommand,
    FailPaymentCommand,
    PaymentAlreadyApproved,
    PaymentAlreadyFailed,
    PaymentApproved,
    PaymentFailed,
    PaymentIdempotencyConflict,
    PaymentOrderMismatch,
    PaymentOrderNotFound,
    PaymentStore,
    approval_should_publish,
    failure_should_publish,
)

SERVICE_NAME: Final = "payment-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class ApprovePaymentContext:
    user_id: UserId
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class ReadPaymentContext:
    user_id: UserId


def create_app(
    repository: PaymentRepository | None = None,
    event_publisher: PaymentEventPublisher | None = None,
) -> FastAPI:
    resources = (
        AppResources(
            repository=repository,
            event_publisher=PaymentEventPublisherRef(
                event_publisher or NoopPaymentEventPublisher(),
            ),
        )
        if repository is not None
        else resources_from_env()
    )
    app = FastAPI(
        title="DropMong Payment Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    payment_metrics = PaymentMetrics(SERVICE_NAME, SERVICE_VERSION, SERVICE_ENVIRONMENT)

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    def readyz() -> ReadinessResponse:
        return ReadinessResponse(
            status="ready",
            service=SERVICE_NAME,
            checks={"payments": "ok", "order_created_handler": "ok"},
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return payment_metrics.render()

    @app.post(
        "/payments/mock-approvals",
        response_model=PaymentResponse,
        status_code=status.HTTP_201_CREATED,
    )
    async def approve_mock_payment(
        payload: ApprovePaymentRequest,
        context: Annotated[ApprovePaymentContext, Depends(approve_payment_context)],
    ) -> PaymentResponse:
        result = await resources.repository.approve_mock_payment(
            ApprovePaymentCommand(
                user_id=context.user_id,
                order_id=OrderId(payload.orderId),
                amount=payload.amount,
                method=payload.method,
                idempotency_key=context.idempotency_key,
            ),
        )
        publishable_payment = approval_should_publish(result)
        if publishable_payment is not None:
            await resources.event_publisher.publish_payment_approved(publishable_payment)
        match result:
            case PaymentApproved(payment=payment):
                payment_metrics.record_payment_approved()
                return PaymentResponse(data=payment)
            case PaymentAlreadyApproved(payment=payment):
                return PaymentResponse(data=payment)
            case PaymentOrderNotFound():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="order is not ready for payment",
                )
            case PaymentOrderMismatch():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="payment request does not match order",
                )
            case PaymentIdempotencyConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="idempotency key reused with different payment request",
                )
            case unreachable:
                assert_never(unreachable)

    @app.post(
        "/payments/mock-failures",
        response_model=PaymentResponse,
        status_code=status.HTTP_201_CREATED,
    )
    async def fail_mock_payment(
        payload: FailPaymentRequest,
        context: Annotated[ApprovePaymentContext, Depends(approve_payment_context)],
    ) -> PaymentResponse:
        result = await resources.repository.fail_mock_payment(
            FailPaymentCommand(
                user_id=context.user_id,
                order_id=OrderId(payload.orderId),
                amount=payload.amount,
                method=payload.method,
                idempotency_key=context.idempotency_key,
                reason=payload.reason,
            ),
        )
        publishable_payment = failure_should_publish(result)
        if publishable_payment is not None:
            await resources.event_publisher.publish_payment_failed(publishable_payment)
        match result:
            case PaymentFailed(payment=payment):
                payment_metrics.record_payment_failed()
                return PaymentResponse(data=payment)
            case PaymentAlreadyFailed(payment=payment):
                return PaymentResponse(data=payment)
            case PaymentOrderNotFound():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="order is not ready for payment",
                )
            case PaymentOrderMismatch():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="payment request does not match order",
                )
            case PaymentIdempotencyConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="idempotency key reused with different payment request",
                )
            case unreachable:
                assert_never(unreachable)

    @app.get("/payments/{payment_id}", response_model=PaymentResponse)
    async def get_payment(
        payment_id: str,
        context: Annotated[ReadPaymentContext, Depends(read_payment_context)],
    ) -> PaymentResponse:
        payment = await resources.repository.get_payment(PaymentId(payment_id))
        if payment is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail="payment not found",
            )
        if payment.userId != context.user_id:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="payment owner mismatch",
            )
        return PaymentResponse(data=payment)

    return app


def approve_payment_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id", min_length=1, max_length=64)],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
    idempotency_key: Annotated[
        str,
        Header(alias="Idempotency-Key", min_length=1, max_length=128),
    ],
) -> ApprovePaymentContext:
    match x_user_role:
        case UserRole.CUSTOMER:
            return ApprovePaymentContext(
                user_id=UserId(x_user_id),
                idempotency_key=IdempotencyKey(idempotency_key),
            )
        case UserRole.OWNER | UserRole.ADMIN:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="customer role required",
            )
        case unreachable:
            assert_never(unreachable)


def read_payment_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id", min_length=1, max_length=64)],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
) -> ReadPaymentContext:
    match x_user_role:
        case UserRole.CUSTOMER:
            return ReadPaymentContext(user_id=UserId(x_user_id))
        case UserRole.OWNER | UserRole.ADMIN:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="customer role required",
            )
        case unreachable:
            assert_never(unreachable)


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
