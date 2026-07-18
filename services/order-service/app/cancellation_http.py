from dataclasses import dataclass
from typing import Annotated
from uuid import UUID

from fastapi import APIRouter, Depends, Header, Path, status
from observability import HttpError

from app.cancellations import (
    CancellationAlreadyRequested,
    CancellationIdempotencyConflict,
    CancellationNotAllowed,
    CancellationOrderMissing,
    CancellationOwnerMismatch,
    CancellationRequested,
    RequestCancellationCommand,
)
from app.cancellation_status_http import cancellation_status_router
from app.models import (
    CancelOrderRequest,
    CancellationResponse,
    ErrorResponse,
    IdempotencyKey,
    OrderId,
    UserId,
)
from app.repository import OrderRepository


@dataclass(frozen=True, slots=True)
class CancellationContext:
    user_id: UserId
    idempotency_key: IdempotencyKey


def cancellation_router(repository: OrderRepository) -> APIRouter:
    router = APIRouter()
    router.include_router(cancellation_status_router(repository))

    @router.post(
        "/orders/{order_id}/cancellations",
        response_model=CancellationResponse,
        status_code=status.HTTP_202_ACCEPTED,
        operation_id="cancelOrder",
        summary="배송 전 주문 취소 및 전액 환불 요청",
        description=(
            "CONFIRMED 주문의 배송이 시작되기 전에 취소를 접수한다. 같은 "
            "Idempotency-Key와 같은 reason의 재실행은 최초 접수 결과를 그대로 "
            "반환한다."
        ),
        responses={
            status.HTTP_202_ACCEPTED: {
                "model": CancellationResponse,
                "description": (
                    "취소와 전액 환불이 접수되었다. 멱등 재실행은 같은 취소 "
                    "접수 결과를 반환한다."
                ),
                "x-idempotent-replay": True,
            },
            status.HTTP_401_UNAUTHORIZED: {
                "model": ErrorResponse,
                "description": "Missing or invalid bearer token.",
            },
            status.HTTP_403_FORBIDDEN: {
                "model": ErrorResponse,
                "description": "Authenticated user is not allowed.",
            },
            status.HTTP_404_NOT_FOUND: {
                "model": ErrorResponse,
                "description": "Order not found.",
            },
            status.HTTP_409_CONFLICT: {
                "model": ErrorResponse,
                "description": "Cancellation conflicts with order state.",
            },
            status.HTTP_422_UNPROCESSABLE_CONTENT: {
                "model": ErrorResponse,
                "description": "Cancellation request is invalid.",
            },
            status.HTTP_500_INTERNAL_SERVER_ERROR: {
                "model": ErrorResponse,
                "description": "Unexpected server error.",
            },
        },
    )
    async def cancel_order(
        order_id: Annotated[str, Path(min_length=1, max_length=64)],
        payload: CancelOrderRequest,
        context: Annotated[
            CancellationContext,
            Depends(cancellation_context),
        ],
    ) -> CancellationResponse:
        result = await repository.request_cancellation(
            RequestCancellationCommand(
                order_id=OrderId(order_id),
                user_id=context.user_id,
                idempotency_key=context.idempotency_key,
                reason=payload.reason,
            ),
        )
        match result:
            case CancellationRequested(cancellation=cancellation) | (
                CancellationAlreadyRequested(cancellation=cancellation)
            ):
                return CancellationResponse(data=cancellation)
            case CancellationIdempotencyConflict():
                raise HttpError(
                    status.HTTP_409_CONFLICT,
                    "cancellation.idempotency_conflict",
                    ("idempotency key reused with different cancellation request"),
                    {"orderId": order_id},
                )
            case CancellationOrderMissing():
                raise HttpError(
                    status.HTTP_404_NOT_FOUND,
                    "order.not_found",
                    "order not found",
                    {"orderId": order_id},
                )
            case CancellationOwnerMismatch():
                raise HttpError(
                    status.HTTP_403_FORBIDDEN,
                    "cancellation.forbidden",
                    "order owner mismatch",
                    {"orderId": order_id},
                )
            case CancellationNotAllowed():
                raise HttpError(
                    status.HTTP_409_CONFLICT,
                    "cancellation.not_allowed",
                    "order is not eligible for cancellation",
                    {"orderId": order_id},
                )
            case unreachable:
                assert_never(unreachable)

    return router


def cancellation_context(
    x_user_id: Annotated[
        UUID,
        Header(
            alias="X-User-Id",
            description=(
                "Authenticated user id forwarded by the trusted gateway in local E2E."
            ),
        ),
    ],
    idempotency_key: Annotated[
        str,
        Header(alias="Idempotency-Key", min_length=1, max_length=128),
    ],
    _request_id: Annotated[
        str | None,
        Header(
            alias="X-Request-Id",
            description=(
                "Request correlation id. If absent, the server may generate one."
            ),
        ),
    ] = None,
    _traceparent: Annotated[
        str | None,
        Header(
            alias="traceparent",
            description="W3C trace context header.",
        ),
    ] = None,
) -> CancellationContext:
    return CancellationContext(
        user_id=UserId(str(x_user_id)),
        idempotency_key=IdempotencyKey(idempotency_key),
    )
