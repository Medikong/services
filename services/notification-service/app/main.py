import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from prometheus_client import CollectorRegistry
from server.operational import register_operational_handlers

from app.config import settings
from app.consumers.kafka_consumer import consume_events
from app.database import connect_db, close_db
from app.metrics import configure_notification_metrics
from app.observability import configure_app_observability
from app.routers import notifications

_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS = 5.0


def _configure_notification_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """notification-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_notification_metrics(
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
    await connect_db()
    consumer_stop_event = asyncio.Event()
    consumer_task = asyncio.create_task(consume_events(consumer_stop_event))
    app.state.consumer_stop_event = consumer_stop_event
    app.state.consumer_task = consumer_task
    try:
        yield
    finally:
        await _stop_background_task(consumer_task, consumer_stop_event)
        app.state.consumer_stop_event = None
        app.state.consumer_task = None
        close_db()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
app.state.consumer_stop_event = None
app.state.consumer_task = None
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
