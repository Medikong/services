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
from prometheus_client import CollectorRegistry
from server import (
    ServiceReadiness,
    register_http_metrics,
    register_service_readiness,
    render_metrics,
)

from app.counter_repository import DropInterestCounterRepository
from app.counter_store import DropInterestCounterStore
from app.db import AppResources, lifespan_for, resources_from_env
from app.messaging import InterestEventPublisher, NoopInterestEventPublisher
from app.models import (
    DropId,
    DropInterestStatsResponse,
    HealthResponse,
    Interest,
    InterestListItem,
    InterestListResponse,
    InterestResponse,
    InterestStatus,
    PageInfo,
    ReadinessResponse,
    TrendingRankingListResponse,
    UpcomingRankingListResponse,
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
from app.view_repository import DropViewRankingRepository, DropViewRepository
from app.view_store import DropViewRankingStore, DropViewStore

SERVICE_NAME: Final = "interest-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


@dataclass(frozen=True, slots=True)
class AuthenticatedUser:
    user_id: UserId


@dataclass(frozen=True, slots=True)
class AuthenticatedOperator:
    user_id: UserId


def create_app(
    repository: InterestRepository | None = None,
    counter_repository: DropInterestCounterRepository | None = None,
    view_repository: DropViewRepository | None = None,
    view_ranking_repository: DropViewRankingRepository | None = None,
    event_publisher: InterestEventPublisher | None = None,
) -> FastAPI:
    if repository is not None:
        view_store = DropViewStore()
        resources = AppResources(
            repository=repository,
            counter_repository=counter_repository or DropInterestCounterStore(),
            view_repository=view_repository or view_store,
            view_ranking_repository=view_ranking_repository
            or DropViewRankingStore(view_store),
            event_publisher=event_publisher or NoopInterestEventPublisher(),
        )
    else:
        resources = resources_from_env()
    app = FastAPI(
        title="DropMong Interest Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(resources),
    )
    common_metrics, service_readiness = _configure_observability(app)

    @app.get("/healthz", response_model=HealthResponse)
    def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz", response_model=ReadinessResponse)
    def readyz() -> ReadinessResponse:
        service_readiness.set(True)
        return ReadinessResponse(
            status="ready",
            service=SERVICE_NAME,
            checks={"interests": "ok"},
            timestamp=_utc_now(),
        )

    @app.get("/metrics", response_class=PlainTextResponse)
    def metrics() -> str:
        return render_metrics(common_metrics)

    @app.put("/v1/users/me/interests/{dropId}", response_model=InterestResponse)
    async def add_interest(
        dropId: str,
        user: Annotated[AuthenticatedUser, Depends(authenticated_user)],
    ) -> InterestResponse:
        drop_id = DropId(dropId)
        result = await resources.repository.upsert_status(
            ToggleInterestCommand(
                user_id=user.user_id,
                drop_id=drop_id,
                target_status=InterestStatus.ACTIVE,
            ),
        )
        if isinstance(result, InterestChanged):
            # 찜 카운터 반영 Consumer(CMD.A.07-05)를 별도 Kafka 컨슈머로 두지 않고 같은 요청 안에서
            # 바로 반영한다 — Interest와 트랜잭션은 분리돼 있어(다른 테이블) 정합성 레벨 분리
            # 원칙(RULE.A.07-03)은 유지되고, 이 샌드박스엔 실제 소비 가능한 브로커가 없다.
            await resources.counter_repository.increment(drop_id)
            await resources.event_publisher.publish_interest_added(
                user.user_id, drop_id
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
        drop_id = DropId(dropId)
        result = await resources.repository.upsert_status(
            ToggleInterestCommand(
                user_id=user.user_id,
                drop_id=drop_id,
                target_status=InterestStatus.INACTIVE,
            ),
        )
        if isinstance(result, InterestChanged):
            await resources.counter_repository.decrement(drop_id)
            await resources.event_publisher.publish_interest_removed(
                user.user_id, drop_id
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
        items = [
            InterestListItem(dropId=interest.dropId, addedAt=interest.updatedAt)
            for interest in interests
        ]
        next_cursor = items[-1].dropId if has_next and items else None
        return InterestListResponse(
            data=tuple(items),
            pageInfo=PageInfo(nextCursor=next_cursor, hasNext=has_next),
        )

    @app.post(
        "/v1/drops/{dropId}/views",
        status_code=status.HTTP_204_NO_CONTENT,
    )
    async def record_drop_view(
        dropId: str,
        user: Annotated[AuthenticatedUser, Depends(authenticated_user)],
    ) -> None:
        await resources.view_repository.record_view(DropId(dropId), user.user_id)

    @app.get("/v1/rankings/drops/upcoming", response_model=UpcomingRankingListResponse)
    async def list_upcoming_ranking(
        limit: int = Query(default=20, ge=1, le=100),
        cursor: str | None = Query(default=None),
    ) -> UpcomingRankingListResponse:
        items, has_next = await resources.counter_repository.list_by_interest_count(
            limit=limit,
            cursor=cursor,
        )
        next_cursor = str(_offset_after(cursor, len(items))) if has_next else None
        return UpcomingRankingListResponse(
            data=tuple(items),
            pageInfo=PageInfo(nextCursor=next_cursor, hasNext=has_next),
        )

    @app.get("/v1/rankings/drops/trending", response_model=TrendingRankingListResponse)
    async def list_trending_ranking(
        limit: int = Query(default=20, ge=1, le=100),
        cursor: str | None = Query(default=None),
    ) -> TrendingRankingListResponse:
        (
            items,
            has_next,
            bucket_start,
        ) = await resources.view_ranking_repository.get_latest_bucket(
            limit=limit,
            cursor=cursor,
        )
        next_cursor = str(items[-1].rank) if has_next and items else None
        return TrendingRankingListResponse(
            data=tuple(items),
            pageInfo=PageInfo(nextCursor=next_cursor, hasNext=has_next),
            bucketStart=bucket_start,
        )

    @app.get(
        "/v1/operator/drops/{dropId}/interest-stats",
        response_model=DropInterestStatsResponse,
    )
    async def get_drop_interest_stats(
        dropId: str,
        _operator: Annotated[AuthenticatedOperator, Depends(authenticated_operator)],
    ) -> DropInterestStatsResponse:
        stats = await resources.counter_repository.get(DropId(dropId))
        if stats is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND, detail="drop not found"
            )
        return DropInterestStatsResponse(data=stats)

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
    app.add_middleware(RequestIdMiddleware, **request_id_middleware_options())
    app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)
    return metrics_registry, readiness


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


def authenticated_operator(
    x_user_id: Annotated[str, Header(alias="X-User-Id")],
    x_user_role: Annotated[UserRole, Header(alias="X-User-Role")],
) -> AuthenticatedOperator:
    # API.A.07-07 1차 스코프: role=operator만 허용한다(브랜드 운영자는 소유권 검증 방식 확정 전까지 범위 밖).
    match x_user_role:
        case UserRole.OPERATOR:
            return AuthenticatedOperator(user_id=UserId(x_user_id))
        case UserRole.CUSTOMER | UserRole.ADMIN:
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail="operator role required",
            )
        case unreachable:
            assert_never(unreachable)


def _offset_after(cursor: str | None, page_size: int) -> int:
    current_offset = int(cursor) if cursor is not None else 0
    return current_offset + page_size


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
