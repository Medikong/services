from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from prometheus_client import CollectorRegistry
from server.operational import register_operational_handlers

from app.config import settings
from app.database import connect_db, close_db
from app.metrics import configure_notification_metrics
from app.observability import configure_app_observability
from app.routers import notifications


def _configure_notification_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """notification-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_notification_metrics(
        registry,
        service_name=settings.service_name,
        service_environment=service_environment,
    )


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """HTTP 프로세스가 보유한 MongoDB 연결을 앱 종료 시 정리한다."""
    await connect_db()
    try:
        yield
    finally:
        close_db()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
configure_app_observability(app, observability_config)
register_operational_handlers(
    app,
    service_name=settings.service_name,
    service_version=observability_config.service_version,
    service_environment=observability_config.service_environment,
    readiness_checks={},
    readiness_success_status="ok",
    readiness_failure_status="failed",
    include_readiness_checks=False,
    configure_metrics=lambda registry: _configure_notification_service_metrics(
        registry,
        service_environment=observability_config.service_environment,
    ),
)
app.include_router(notifications.router)


# 기존 health 엔드포인트 유지
@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}
