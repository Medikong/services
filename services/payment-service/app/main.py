import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never
from uuid import UUID

from fastapi import Depends, FastAPI, Header, HTTPException, Response, status
from fastapi.responses import ORJSONResponse, PlainTextResponse
from middleware import is_safe_request_id
from observability import (
    REQUEST_ID_HEADER,
    RequestIdMiddleware,
    configure_process_observability,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
)

from app.db import AppResources, lifespan_for, resources_from_env
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
)
from app.repository import PaymentRepository
from app.readiness import payment_readiness
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
    PaymentOrderOwnerMismatch,
    PaymentTerminalConflict,
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
) -> FastAPI:
    payment_metrics = PaymentMetrics(SERVICE_NAME, SERVICE_VERSION, SERVICE_ENVIRONMENT)
    resources = (
        AppResources(
            repository=repository,
            metrics=payment_metrics,
        )
        if repository is not None
        else resources_from_env(payment_metrics)
    )
    app = FastAPI(
        title="DropMong Payment Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    _configure_observability(app)

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    async def readyz(response: Response) -> ReadinessResponse:
        return await payment_readiness(resources, response, SERVICE_NAME)

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
            case PaymentOrderOwnerMismatch():
                raise HTTPException(
                    status_code=status.HTTP_403_FORBIDDEN,
                    detail="order owner mismatch",
                )
            case PaymentIdempotencyConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="idempotency key reused with different payment request",
                )
            case PaymentTerminalConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="order already has a terminal payment",
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
            case PaymentOrderOwnerMismatch():
                raise HTTPException(
                    status_code=status.HTTP_403_FORBIDDEN,
                    detail="order owner mismatch",
                )
            case PaymentIdempotencyConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="idempotency key reused with different payment request",
                )
            case PaymentTerminalConflict():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="order already has a terminal payment",
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


def _configure_observability(app: FastAPI) -> None:
    config = observability_config_from_env(SERVICE_NAME, env=os.environ)
    configure_process_observability(config)
    app.add_middleware(
        RequestIdMiddleware,
        header_name=REQUEST_ID_HEADER,
        update_request_header=True,
        validator=is_safe_request_id,
    )
    app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)


def approve_payment_context(
    x_user_id: Annotated[UUID, Header(alias="X-User-Id")],
    idempotency_key: Annotated[
        str,
        Header(alias="Idempotency-Key", min_length=1, max_length=128),
    ],
) -> ApprovePaymentContext:
    return ApprovePaymentContext(
        user_id=UserId(str(x_user_id)),
        idempotency_key=IdempotencyKey(idempotency_key),
    )


def read_payment_context(
    x_user_id: Annotated[UUID, Header(alias="X-User-Id")],
) -> ReadPaymentContext:
    return ReadPaymentContext(user_id=UserId(str(x_user_id)))


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
