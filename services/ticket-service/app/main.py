import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from prometheus_client import CollectorRegistry
from server.operational import register_operational_handlers

from app import models
from app.config import settings
from app.consumers.kafka_consumer import EventHandlers, consume_events
from app.database import SessionLocal, engine
from app.kafka import KafkaProducer, create_producer
from app.metrics import configure_ticket_metrics
from app.observability import configure_app_observability
from app.routers import tickets
from app.services.ticket_service import PaymentApprovedEventHandler

models.Base.metadata.create_all(bind=engine)

_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS = 5.0


def kafka_event_handlers(kafka_producer: KafkaProducer) -> EventHandlers:
    return {settings.payment_approved_topic: PaymentApprovedEventHandler(SessionLocal, kafka_producer)}


def _configure_ticket_service_metrics(registry: CollectorRegistry, *, service_environment: str) -> None:
    """ticket-service 전용 Prometheus metric을 운영 registry에 등록한다."""
    configure_ticket_metrics(
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
    consumer_stop_event: asyncio.Event | None = None
    consumer_task: asyncio.Task[None] | None = None
    try:
        if producer is not None:
            await producer.start()
        consumer_stop_event = asyncio.Event()
        consumer_task = asyncio.create_task(
            consume_events(
                consumer_stop_event,
                bootstrap_servers=settings.kafka_bootstrap_servers,
                group_id=settings.kafka_group_id,
                service_name=settings.service_name,
                handlers=kafka_event_handlers(producer),
            )
        )
        app.state.consumer_stop_event = consumer_stop_event
        app.state.consumer_task = consumer_task
        yield
    finally:
        await _stop_background_task(consumer_task, consumer_stop_event)
        app.state.consumer_stop_event = None
        app.state.consumer_task = None
        if producer is not None:
            await producer.stop()
        app.state.kafka_producer = None
        engine.dispose()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
app.state.kafka_producer = None
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
    configure_metrics=lambda registry: _configure_ticket_service_metrics(
        registry,
        service_environment=observability_config.service_environment,
    ),
)
app.include_router(tickets.router)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}
