from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, status
from observability import register_error_handlers
from prometheus_client import CollectorRegistry
from server.operational import register_operational_handlers, sqlalchemy_readiness_check

from app import models
from app.config import settings
from app.database import engine
from app.metrics import configure_payment_metrics
from app.observability import configure_app_observability
from app.routes.payments import router as payments_router
from app.schema_migrations import run_schema_migrations


models.Base.metadata.create_all(bind=engine)
run_schema_migrations(engine)


def _configure_payment_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """payment-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_payment_metrics(
        registry,
        service_name=settings.service_name,
        service_environment=service_environment,
    )


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """HTTP 프로세스가 보유한 DB 연결 풀을 앱 종료 시 정리한다."""
    try:
        yield
    finally:
        engine.dispose()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
configure_app_observability(app, observability_config)
register_error_handlers(
    app,
    service_name=settings.service_name,
    domain="payment",
    http_error_code_for_status=lambda status_code: _error_code_for_status(status_code),
)
register_operational_handlers(
    app,
    service_name=settings.service_name,
    service_version=observability_config.service_version,
    service_environment=observability_config.service_environment,
    readiness_checks={"database": sqlalchemy_readiness_check(engine)},
    configure_metrics=lambda registry: _configure_payment_service_metrics(
        registry,
        service_environment=observability_config.service_environment,
    ),
    include_timestamp=True,
)
app.include_router(payments_router)


@app.get("/health")
def health() -> dict[str, str]:
    """기존 호환용 health endpoint 응답을 반환한다."""
    return {"status": "ok", "service": settings.service_name}


def _error_code_for_status(status_code: int) -> str:
    """HTTP 상태 코드를 payment-service 오류 코드로 변환한다."""
    if status_code == status.HTTP_401_UNAUTHORIZED:
        return "auth.invalid_token"
    if status_code == status.HTTP_403_FORBIDDEN:
        return "auth.forbidden"
    if status_code == status.HTTP_404_NOT_FOUND:
        return "payment.not_found"
    if status_code == status.HTTP_503_SERVICE_UNAVAILABLE:
        return "service.unavailable"
    return "request.failed"
