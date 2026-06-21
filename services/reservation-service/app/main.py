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
from app.kafka import create_producer
from app.metrics import configure_reservation_metrics
from app.observability import configure_app_observability
from app.routers import router as reservation_router


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


def _configure_reservation_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """reservation-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_reservation_metrics(
        registry,
        service_name=settings.service_name,
        service_environment=service_environment,
    )


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """안전한 종료를 위해 앱이 붙잡고 있는 작업과 연결을 lifespan에서 관리한다."""
    producer = create_producer()
    app.state.kafka_producer = producer
    if producer is not None:
        await producer.start()
    try:
        yield
    finally:
        if producer is not None:
            await producer.stop()
        app.state.kafka_producer = None
        engine.dispose()


def create_app() -> FastAPI:
    init_db()
    observability_config = settings.observability_config()
    app = FastAPI(title=settings.service_name, lifespan=lifespan)
    app.state.kafka_producer = None
    configure_app_observability(app, observability_config)
    register_exception_handlers(app)
    register_operational_handlers(
        app,
        service_name=settings.service_name,
        service_version=observability_config.service_version,
        service_environment=observability_config.service_environment,
        readiness_checks=_readiness_checks(),
        configure_metrics=lambda registry: _configure_reservation_service_metrics(
            registry,
            service_environment=observability_config.service_environment,
        ),
    )

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    app.include_router(reservation_router)

    return app
