import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final

from fastapi import Depends, FastAPI, Header, Query, Response, status
from fastapi.responses import ORJSONResponse, PlainTextResponse
from observability import (
    RequestIdMiddleware,
    configure_process_observability,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
    request_id_middleware_options,
)

from app.db import AppResources, lifespan_for, resources_from_env
from app.metrics import NotificationMetrics
from app.models import (
    HealthResponse,
    NotificationId,
    NotificationListResponse,
    ReadinessResponse,
    UserId,
)
from app.repository import NotificationRepository

SERVICE_NAME: Final = "notification-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class ReadNotificationsContext:
    user_id: UserId


def create_app(
    repository: NotificationRepository | None = None,
    notification_metrics: NotificationMetrics | None = None,
) -> FastAPI:
    metrics_instance = notification_metrics or NotificationMetrics(
        SERVICE_NAME,
        SERVICE_VERSION,
        SERVICE_ENVIRONMENT,
    )
    resources = (
        AppResources(repository=repository, notification_metrics=metrics_instance)
        if repository is not None
        else resources_from_env(metrics_instance)
    )
    app = FastAPI(
        title="DropMong Notification Service API",
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
        repository_ready = await resources.repository.is_ready()
        if not repository_ready:
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
        return ReadinessResponse(
            status="ready" if repository_ready else "not_ready",
            service=SERVICE_NAME,
            checks={
                "notifications": "ok" if repository_ready else "migration_required",
                "notification_requested_handler": "ok",
            },
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return resources.notification_metrics.render()

    @app.get("/notifications", response_model=NotificationListResponse)
    async def list_notifications(
        context: Annotated[
            ReadNotificationsContext, Depends(read_notifications_context)
        ],
        limit: Annotated[int, Query(ge=1, le=100)] = 20,
        cursor: Annotated[str | None, Query(max_length=128)] = None,
    ) -> NotificationListResponse:
        page = await resources.repository.list_notifications(
            context.user_id,
            limit,
            NotificationId(cursor) if cursor is not None else None,
        )
        return NotificationListResponse(
            data=page.notifications,
            pageInfo=page.page_info,
        )

    return app


def _configure_observability(app: FastAPI) -> None:
    config = observability_config_from_env(SERVICE_NAME, env=os.environ)
    configure_process_observability(config)
    app.add_middleware(RequestIdMiddleware, **request_id_middleware_options())
    app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)


def read_notifications_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id", min_length=1, max_length=64)],
) -> ReadNotificationsContext:
    return ReadNotificationsContext(user_id=UserId(x_user_id))


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
