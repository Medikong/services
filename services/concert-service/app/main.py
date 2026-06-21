from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from prometheus_client import CollectorRegistry
from server.operational import (
    ReadinessCheck,
    register_operational_handlers,
    required_settings_readiness_check,
    sqlalchemy_readiness_check,
)

from app.config import settings
from app.database import engine, init_db
from app.exceptions import register_exception_handlers
from app.metrics import configure_concert_metrics
from app.observability import configure_app_observability
from app.routers import router as concert_router


def _readiness_checks() -> dict[str, ReadinessCheck]:
    return {
        "config": required_settings_readiness_check(
            {
                "service_name": settings.service_name,
                "database_url": settings.database_url,
            }
        ),
        "database": sqlalchemy_readiness_check(engine),
    }


def _configure_concert_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """concert-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_concert_metrics(
        registry,
        service_name=settings.service_name,
        service_environment=service_environment,
    )


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """앱 종료 시 DB 연결 풀이 남지 않도록 lifespan에서 정리한다."""
    try:
        yield
    finally:
        engine.dispose()


def create_app() -> FastAPI:
    init_db()
    observability_config = settings.observability_config()
    app = FastAPI(title=settings.service_name, lifespan=lifespan)
    configure_app_observability(app, observability_config)
    register_exception_handlers(app)
    register_operational_handlers(
        app,
        service_name=settings.service_name,
        service_version=observability_config.service_version,
        service_environment=observability_config.service_environment,
        readiness_checks=_readiness_checks(),
        configure_metrics=lambda registry: _configure_concert_service_metrics(
            registry,
            service_environment=observability_config.service_environment,
        ),
    )

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    app.include_router(concert_router)

    return app
