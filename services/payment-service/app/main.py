import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI, status
from observability import register_error_handlers
from prometheus_client import CollectorRegistry
from server.operational import register_operational_handlers, sqlalchemy_readiness_check

from app import models
from app.config import settings
from app.database import SessionLocal, engine
from app.kafka import create_producer
from app.metrics import configure_payment_metrics
from app.observability import configure_app_observability
from app.routes.payments import router as payments_router
from app.services.payment_events import run_payment_event_dispatcher


models.Base.metadata.create_all(bind=engine)

_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS = 5.0


def _configure_payment_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """payment-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_payment_metrics(
        registry,
        service_name=settings.service_name,
        service_environment=service_environment,
    )


async def _stop_background_task(task: asyncio.Task[None] | None, stop_event: asyncio.Event | None) -> None:
    if task is None or stop_event is None:
        return

    stop_event.set()
    try:
        await asyncio.wait_for(asyncio.shield(task), timeout=_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS)
    except TimeoutError:
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    """안전한 종료를 위해 앱이 붙잡고 있는 작업과 연결을 lifespan에서 관리한다."""
    producer = create_producer()
    app.state.kafka_producer = producer
    dispatcher_stop_event: asyncio.Event | None = None
    dispatcher_task: asyncio.Task[None] | None = None
    try:
        if producer is not None:
            await producer.start()
            dispatcher_stop_event = asyncio.Event()
            dispatcher_task = asyncio.create_task(
                run_payment_event_dispatcher(
                    dispatcher_stop_event,
                    session_factory=SessionLocal,
                    kafka_producer=producer,
                    interval_seconds=settings.payment_event_dispatch_interval_seconds,
                    batch_size=settings.payment_event_dispatch_batch_size,
                    max_attempts=settings.payment_event_dispatch_max_attempts,
                )
            )
            app.state.payment_event_dispatcher_stop_event = dispatcher_stop_event
            app.state.payment_event_dispatcher_task = dispatcher_task
        yield
    finally:
        await _stop_background_task(dispatcher_task, dispatcher_stop_event)
        app.state.payment_event_dispatcher_stop_event = None
        app.state.payment_event_dispatcher_task = None
        if producer is not None:
            await producer.stop()
        app.state.kafka_producer = None
        engine.dispose()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
app.state.kafka_producer = None
app.state.payment_event_dispatcher_stop_event = None
app.state.payment_event_dispatcher_task = None
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
