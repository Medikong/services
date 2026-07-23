import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never
from uuid import UUID

from fastapi import Depends, FastAPI, Header, HTTPException, Request, Response, status
from fastapi.responses import JSONResponse, ORJSONResponse, PlainTextResponse
from middleware import is_safe_request_id
from prometheus_client import CollectorRegistry
from observability import (
    REQUEST_ID_HEADER,
    RequestIdMiddleware,
    configure_process_observability,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
    error_response,
    record_exception,
    register_error_handlers,
)
from server import (
    ServiceReadiness,
    register_http_metrics,
    register_service_readiness,
    render_metrics,
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
)
from app.db import AppResources, lifespan_for, resources_from_env
from app.cancellation_http import cancellation_router
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
    order_metrics = OrderMetrics(SERVICE_NAME, SERVICE_VERSION, SERVICE_ENVIRONMENT)
    resources = (
        AppResources(repository=repository, metrics=order_metrics)
        if repository is not None
        else resources_from_env(order_metrics)
    )
    app = FastAPI(
        title="DropMong Order Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    common_metrics, service_readiness = _configure_observability(app)
    register_error_handlers(app, service_name=SERVICE_NAME, domain="order")

    @app.exception_handler(Exception)
    async def handle_unexpected_error(
        request: Request,
        exc: Exception,
    ) -> JSONResponse:
        record_exception(exc, service_name=SERVICE_NAME)
        return error_response(
            request,
            status.HTTP_500_INTERNAL_SERVER_ERROR,
            "order.internal_error",
            "Unexpected server error.",
        )

    app.include_router(cancellation_router(resources.repository))

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
        service_readiness.set(migration_is_current)
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
        return render_metrics(common_metrics) + order_metrics.render()

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


def _configure_observability(
    app: FastAPI,
) -> tuple[CollectorRegistry, ServiceReadiness]:
    config = observability_config_from_env(
        SERVICE_NAME,
        env=os.environ,
        default_service_version=SERVICE_VERSION,
        default_service_environment=SERVICE_ENVIRONMENT,
    )
    configure_process_observability(config)
    metrics_registry = register_http_metrics(
        app,
        service_name=SERVICE_NAME,
        service_version=SERVICE_VERSION,
        service_environment=SERVICE_ENVIRONMENT,
    )
    readiness = register_service_readiness(
        metrics_registry,
        service_name=SERVICE_NAME,
        service_version=SERVICE_VERSION,
        service_environment=SERVICE_ENVIRONMENT,
    )
    app.add_middleware(
        RequestIdMiddleware,
        header_name=REQUEST_ID_HEADER,
        update_request_header=True,
        validator=is_safe_request_id,
    )
    app.middleware("http")(create_request_log_middleware(config))

    @app.middleware("http")
    async def add_service_version_header(request: Request, call_next):
        response = await call_next(request)
        response.headers["X-Service-Version"] = SERVICE_VERSION
        return response

    instrument_fastapi_app(app, config)
    return metrics_registry, readiness


def create_order_context(
    x_user_id: Annotated[UUID, Header(alias="X-User-Id")],
    idempotency_key: Annotated[
        str,
        Header(alias="Idempotency-Key", min_length=1, max_length=128),
    ],
) -> CreateOrderContext:
    return CreateOrderContext(
        user_id=UserId(str(x_user_id)),
        idempotency_key=IdempotencyKey(idempotency_key),
    )


def read_order_context(
    x_user_id: Annotated[UUID, Header(alias="X-User-Id")],
) -> ReadOrderContext:
    return ReadOrderContext(user_id=UserId(str(x_user_id)))


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
