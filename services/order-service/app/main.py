import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never

from fastapi import Depends, FastAPI, Header, HTTPException, Response, status
from fastapi.responses import ORJSONResponse, PlainTextResponse
from observability import (
    RequestIdMiddleware,
    configure_process_observability,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
    request_id_middleware_options,
)

from app.models import (
    CreateOrderRequest,
    DropId,
    HealthResponse,
    IdempotencyKey,
    OrderId,
    OrderResponse,
    ReadinessResponse,
    ProductId,
    UserId,
    UserRole,
)
from app.db import AppResources, lifespan_for, resources_from_env
from app.metrics import OrderMetrics
from app.repository import OrderRepository
from app.schema import database_migration_is_current
from app.store import (
    CreateOrderCommand,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    ProductSoldOut,
    ProductUnavailable,
)

SERVICE_NAME: Final = "order-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class CreateOrderContext:
    user_id: UserId
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class ReadOrderContext:
    user_id: UserId


def create_app(
    repository: OrderRepository | None = None,
) -> FastAPI:
    resources = (
        AppResources(repository=repository)
        if repository is not None
        else resources_from_env()
    )
    app = FastAPI(
        title="DropMong Order Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    order_metrics = OrderMetrics(SERVICE_NAME, SERVICE_VERSION, SERVICE_ENVIRONMENT)
    resources.metrics = order_metrics
    _configure_observability(app)

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    async def readyz(response: Response) -> ReadinessResponse:
        migration_is_current = (
            resources.engine is None
            or await database_migration_is_current(
                resources.engine,
            )
        )
        if not migration_is_current:
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
        return ReadinessResponse(
            status="ready" if migration_is_current else "not_ready",
            service=SERVICE_NAME,
            checks={
                "orders": "ok",
                "payment_approved_handler": "ok",
                "payment_failed_handler": "ok",
                **(
                    {"database_migration": "ok"}
                    if resources.engine is not None and migration_is_current
                    else {}
                ),
                **(
                    {"database_migration": "failed"}
                    if resources.engine is not None and not migration_is_current
                    else {}
                ),
            },
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return order_metrics.render()

    @app.post(
        "/orders",
        response_model=OrderResponse,
        status_code=status.HTTP_201_CREATED,
    )
    async def create_order(
        payload: CreateOrderRequest,
        context: Annotated[CreateOrderContext, Depends(create_order_context)],
    ) -> OrderResponse:
        result = await resources.repository.create_order(
            CreateOrderCommand(
                user_id=context.user_id,
                drop_id=DropId(payload.dropId),
                product_id=ProductId(payload.productId),
                quantity=payload.quantity,
                idempotency_key=context.idempotency_key,
            ),
        )
        match result:
            case OrderCreated(order=order):
                order_metrics.record_order_created()
                return OrderResponse(data=order)
            case OrderAlreadyCreated(order=order):
                order_metrics.record_idempotency_replay()
                return OrderResponse(data=order)
            case OrderIdempotencyConflict():
                order_metrics.record_idempotency_conflict()
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="idempotency key reused with different order request",
                )
            case ProductSoldOut():
                order_metrics.record_sold_out()
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="product sold out",
                )
            case ProductUnavailable():
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="product unavailable",
                )
            case unreachable:
                assert_never(unreachable)

    @app.get("/orders/{order_id}", response_model=OrderResponse)
    async def get_order(
        order_id: str,
        context: Annotated[ReadOrderContext, Depends(read_order_context)],
    ) -> OrderResponse:
        order = await resources.repository.get_order(OrderId(order_id))
        if order is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail="order not found",
            )
        if order.userId != context.user_id:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="order owner mismatch",
            )
        return OrderResponse(data=order)

    return app


def _configure_observability(app: FastAPI) -> None:
    config = observability_config_from_env(SERVICE_NAME, env=os.environ)
    configure_process_observability(config)
    app.add_middleware(RequestIdMiddleware, **request_id_middleware_options())
    app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)


def create_order_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id")],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
    idempotency_key: Annotated[str, Header(alias="Idempotency-Key")],
) -> CreateOrderContext:
    match x_user_role:
        case UserRole.CUSTOMER:
            return CreateOrderContext(
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


def read_order_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id")],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
) -> ReadOrderContext:
    match x_user_role:
        case UserRole.CUSTOMER:
            return ReadOrderContext(user_id=UserId(x_user_id))
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
