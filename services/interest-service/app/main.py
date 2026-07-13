import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Annotated, Final, assert_never

from fastapi import Depends, FastAPI, Header, HTTPException, Query, status
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
from app.models import (
    DropId,
    HealthResponse,
    Interest,
    InterestListItem,
    InterestListResponse,
    InterestResponse,
    InterestStatus,
    PageInfo,
    ReadinessResponse,
    UserId,
    UserRole,
)
from app.repository import InterestRepository
from app.store import (
    InterestChanged,
    InterestToggleConflict,
    InterestUnchanged,
    ToggleInterestCommand,
    ToggleInterestResult,
)

SERVICE_NAME: Final = "interest-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class AuthenticatedUser:
    user_id: UserId


def create_app(repository: InterestRepository | None = None) -> FastAPI:
    resources = (
        AppResources(repository=repository) if repository is not None else resources_from_env()
    )
    app = FastAPI(
        title="DropMong Interest Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    _configure_observability(app)

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    def readyz() -> ReadinessResponse:
        return ReadinessResponse(
            status="ready",
            service=SERVICE_NAME,
            checks={"interests": "ok"},
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

    @app.put("/v1/users/me/interests/{dropId}", response_model=InterestResponse)
    async def add_interest(
        dropId: str,
        user: Annotated[AuthenticatedUser, Depends(authenticated_user)],
    ) -> InterestResponse:
        result = await resources.repository.upsert_status(
            ToggleInterestCommand(
                user_id=user.user_id,
                drop_id=DropId(dropId),
                target_status=InterestStatus.ACTIVE,
            ),
        )
        return InterestResponse(data=_interest_from_result(result))

    @app.delete(
        "/v1/users/me/interests/{dropId}",
        status_code=status.HTTP_204_NO_CONTENT,
    )
    async def remove_interest(
        dropId: str,
        user: Annotated[AuthenticatedUser, Depends(authenticated_user)],
    ) -> None:
        result = await resources.repository.upsert_status(
            ToggleInterestCommand(
                user_id=user.user_id,
                drop_id=DropId(dropId),
                target_status=InterestStatus.INACTIVE,
            ),
        )
        _interest_from_result(result)

    @app.get("/v1/users/me/interests", response_model=InterestListResponse)
    async def list_my_interests(
        user: Annotated[AuthenticatedUser, Depends(authenticated_user)],
        limit: int = Query(default=20, ge=1, le=100),
        cursor: str | None = Query(default=None),
    ) -> InterestListResponse:
        interests, has_next = await resources.repository.list_active_by_user(
            user_id=user.user_id,
            limit=limit,
            cursor=cursor,
        )
        items = [InterestListItem(dropId=interest.dropId, addedAt=interest.updatedAt) for interest in interests]
        next_cursor = items[-1].dropId if has_next and items else None
        return InterestListResponse(
            data=tuple(items),
            pageInfo=PageInfo(nextCursor=next_cursor, hasNext=has_next),
        )

    return app


def _configure_observability(app: FastAPI) -> None:
    config = observability_config_from_env(SERVICE_NAME, env=os.environ)
    configure_process_observability(config)
    app.add_middleware(RequestIdMiddleware, **request_id_middleware_options())
    app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)


def authenticated_user(
    x_user_id: Annotated[str, Header(alias="X-User-Id")],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
) -> AuthenticatedUser:
    match x_user_role:
        case UserRole.CUSTOMER:
            return AuthenticatedUser(user_id=UserId(x_user_id))
        case UserRole.OPERATOR | UserRole.ADMIN:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="customer role required",
            )
        case unreachable:
            assert_never(unreachable)


def _interest_from_result(result: ToggleInterestResult) -> Interest:
    match result:
        case InterestChanged(interest=interest) | InterestUnchanged(interest=interest):
            return interest
        case InterestToggleConflict():
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail="concurrent update conflict, retry the request",
            )
        case unreachable:
            assert_never(unreachable)


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
