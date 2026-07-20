"""Catalog FastAPI composition root."""

import os
from datetime import UTC, datetime
from typing import Annotated, Final

from fastapi import FastAPI, HTTPException, Query, Response, status
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
from prometheus_client import CollectorRegistry
from server import (
    ServiceReadiness,
    register_http_metrics,
    register_service_readiness,
    render_metrics,
)

from app.catalog import CatalogReadiness, DropDetail
from app.db import Database, create_database, lifespan_for
from app.messaging import inventory_consumer_factory
from app.postgres import PostgresCatalogRepository
from app.repository import CatalogRepository
from app.schemas import (
    DropDetailResponse,
    DropListResponse,
    HealthResponse,
    PageInfo,
    ReadinessResponse,
    drop_response,
    drop_summary,
)
from app.store import CatalogStore

SERVICE_NAME: Final = "catalog-service"
SERVICE_VERSION: Final = os.getenv("SERVICE_VERSION", "0.1.0")
SERVICE_ENVIRONMENT: Final = os.getenv("SERVICE_ENVIRONMENT", "local")


def create_app(repository: CatalogRepository | None = None) -> FastAPI:
    """Create the Catalog API with an injected or PostgreSQL repository."""
    database: Database | None = None
    if repository is None:
        database = create_database()
        repository = PostgresCatalogRepository(database.sessions)
    store = CatalogStore(repository)
    consumer_factory = (
        inventory_consumer_factory(
            PostgresCatalogRepository(database.sessions),
            os.getenv("KAFKA_BOOTSTRAP_SERVERS", ""),
        )
        if database is not None
        else None
    )

    app = FastAPI(
        title="DropMong Catalog Service API",
        version=SERVICE_VERSION,
        default_response_class=ORJSONResponse,
        lifespan=lifespan_for(database, consumer_factory),
    )
    common_metrics, service_readiness = _configure_observability(app)

    @app.get("/healthz")
    async def healthz() -> HealthResponse:
        return HealthResponse(status="ok", service=SERVICE_NAME, timestamp=_utc_now())

    @app.get("/readyz")
    async def readyz(response: Response) -> ReadinessResponse:
        readiness = _readiness_response(await store.readiness(), response)
        service_readiness.set(response.status_code == status.HTTP_200_OK)
        return readiness

    @app.get("/metrics", response_class=PlainTextResponse)
    async def metrics() -> str:
        return render_metrics(common_metrics)

    @app.get("/drops")
    async def list_drops(
        limit: Annotated[int, Query(ge=1, le=100)] = 20,
        cursor: Annotated[str | None, Query()] = None,
    ) -> DropListResponse:
        drops = await store.list_drops()
        start = _start_index_after(drops, cursor)
        selected = drops[start : start + limit]
        has_next = start + limit < len(drops)
        next_cursor = selected[-1].id if has_next and selected else None
        return DropListResponse(
            data=tuple(drop_summary(drop) for drop in selected),
            page_info=PageInfo(next_cursor=next_cursor, has_next=has_next),
        )

    @app.get("/drops/{drop_id}")
    async def get_drop(drop_id: str) -> DropDetailResponse:
        drop = await store.get_drop(drop_id)
        if drop is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail="drop not found",
            )
        return DropDetailResponse(data=drop_response(drop))

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
    _ = app.middleware("http")(create_request_log_middleware(config))
    instrument_fastapi_app(app, config)
    return metrics_registry, readiness


def _readiness_response(
    readiness: CatalogReadiness,
    response: Response,
) -> ReadinessResponse:
    match readiness:  # noqa: MATCH_OK
        case CatalogReadiness.READY:
            response.status_code = status.HTTP_200_OK
            readiness_status = "ready"
            checks = {"catalog": "ok"}
        case CatalogReadiness.MIGRATION_REQUIRED:
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
            readiness_status = "not_ready"
            checks = {"catalog": "migration_required"}
        case CatalogReadiness.DATABASE_UNAVAILABLE:
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
            readiness_status = "not_ready"
            checks = {
                "catalog": "migration_required",
                "database": "unavailable",
            }
    return ReadinessResponse(
        status=readiness_status,
        service=SERVICE_NAME,
        checks=checks,
        timestamp=_utc_now(),
    )


def _start_index_after(drops: tuple[DropDetail, ...], cursor: str | None) -> int:
    if cursor is None:
        return 0
    return next(
        (index + 1 for index, drop in enumerate(drops) if drop.id == cursor),
        len(drops),
    )


def _utc_now() -> datetime:
    return datetime.now(UTC)


app = create_app()
