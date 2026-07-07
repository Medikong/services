import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never

from fastapi import Depends, FastAPI, Header, HTTPException, Query, status
from fastapi.responses import ORJSONResponse, PlainTextResponse

from app.db import AppResources, lifespan_for, resources_from_env
from app.models import (
    HealthResponse,
    NotificationListResponse,
    NotificationId,
    ReadinessResponse,
    UserId,
    UserRole,
)
from app.repository import NotificationRepository
from app.store import NotificationStore

SERVICE_NAME: Final = "notification-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class ReadNotificationsContext:
    user_id: UserId


def create_app(repository: NotificationRepository | None = None) -> FastAPI:
    resources = (
        AppResources(repository=repository)
        if repository is not None
        else resources_from_env()
    )
    app = FastAPI(
        title="DropMong Notification Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    def readyz() -> ReadinessResponse:
        return ReadinessResponse(
            status="ready",
            service=SERVICE_NAME,
            checks={"notifications": "ok", "notification_requested_handler": "ok"},
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return (
            "# HELP service_ready Service readiness state. Ready is 1, not ready is 0.\n"
            "# TYPE service_ready gauge\n"
            f'service_ready{{service_name="{SERVICE_NAME}",'
            f'service_version="{SERVICE_VERSION}",'
            f'service_environment="{SERVICE_ENVIRONMENT}"}} 1\n'
        )

    @app.get("/notifications", response_model=NotificationListResponse)
    async def list_notifications(
        context: Annotated[ReadNotificationsContext, Depends(read_notifications_context)],
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


def read_notifications_context(
    x_user_id: Annotated[str, Header(alias="X-User-Id", min_length=1, max_length=64)],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
) -> ReadNotificationsContext:
    match x_user_role:
        case UserRole.CUSTOMER:
            return ReadNotificationsContext(user_id=UserId(x_user_id))
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
