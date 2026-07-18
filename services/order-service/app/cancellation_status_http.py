from dataclasses import dataclass
from typing import Annotated
from uuid import UUID

from fastapi import APIRouter, Depends, Header, Path, status
from observability import HttpError

from app.models import CancellationResponse, ErrorResponse, OrderId, UserId
from app.repository import OrderRepository


@dataclass(frozen=True, slots=True)
class CancellationReadContext:
    user_id: UserId


def cancellation_status_router(repository: OrderRepository) -> APIRouter:
    router = APIRouter()

    @router.get(
        "/orders/{order_id}/cancellations",
        response_model=CancellationResponse,
        operation_id="getOrderCancellation",
        summary="주문 취소 및 환불 상태 조회",
        responses={
            status.HTTP_200_OK: {
                "model": CancellationResponse,
                "description": "현재 취소 및 환불 처리 상태.",
            },
            status.HTTP_401_UNAUTHORIZED: {
                "model": ErrorResponse,
                "description": "Missing or invalid bearer token.",
            },
            status.HTTP_403_FORBIDDEN: {
                "model": ErrorResponse,
                "description": (
                    "Authenticated user is not allowed to access the resource."
                ),
            },
            status.HTTP_404_NOT_FOUND: {
                "model": ErrorResponse,
                "description": "Resource not found.",
            },
            status.HTTP_422_UNPROCESSABLE_CONTENT: {
                "model": ErrorResponse,
                "description": (
                    "Request is syntactically valid but violates domain rules."
                ),
            },
            status.HTTP_500_INTERNAL_SERVER_ERROR: {
                "model": ErrorResponse,
                "description": "Unexpected server error.",
            },
        },
    )
    async def get_order_cancellation(
        order_id: Annotated[str, Path(min_length=1, max_length=64)],
        context: Annotated[
            CancellationReadContext,
            Depends(cancellation_read_context),
        ],
    ) -> CancellationResponse:
        typed_order_id = OrderId(order_id)
        order = await repository.get_order(typed_order_id)
        if order is not None and order.userId != context.user_id:
            raise HttpError(
                status.HTTP_403_FORBIDDEN,
                "cancellation.forbidden",
                "order owner mismatch",
                {"orderId": order_id},
            )
        cancellation = await repository.get_cancellation(
            typed_order_id,
            context.user_id,
        )
        if cancellation is None:
            raise HttpError(
                status.HTTP_404_NOT_FOUND,
                "cancellation.not_found",
                "cancellation not found",
                {"orderId": order_id},
            )
        return CancellationResponse(data=cancellation)

    return router


def cancellation_read_context(
    x_user_id: Annotated[
        UUID,
        Header(
            alias="X-User-Id",
            description=(
                "Authenticated user id forwarded by the trusted gateway in local E2E."
            ),
        ),
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
        Header(alias="traceparent", description="W3C trace context header."),
    ] = None,
) -> CancellationReadContext:
    return CancellationReadContext(user_id=UserId(str(x_user_id)))
