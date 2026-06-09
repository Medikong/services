import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from fastapi import FastAPI
from server.operational import register_operational_handlers

from app import models
from app.config import settings
from app.consumers.kafka_consumer import EventHandlers, consume_events
from app.database import SessionLocal, engine
from app.kafka import KafkaProducer, create_producer
from app.observability import configure_app_observability
from app.routers import tickets
from app.services.ticket_service import PaymentApprovedEventHandler

models.Base.metadata.create_all(bind=engine)


def kafka_event_handlers(kafka_producer: KafkaProducer) -> EventHandlers:
    return {settings.payment_approved_topic: PaymentApprovedEventHandler(SessionLocal, kafka_producer)}


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    producer = app.state.kafka_producer
    if producer is not None:
        await producer.start()
    app.state.consumer_stop_event = asyncio.Event()
    app.state.consumer_task = asyncio.create_task(
        consume_events(
            app.state.consumer_stop_event,
            bootstrap_servers=settings.kafka_bootstrap_servers,
            group_id=settings.kafka_group_id,
            service_name=settings.service_name,
            handlers=kafka_event_handlers(producer),
        )
    )
    try:
        yield
    finally:
        app.state.consumer_stop_event.set()
        app.state.consumer_task.cancel()
        try:
            await app.state.consumer_task
        except asyncio.CancelledError:
            pass
        if producer is not None:
            await producer.stop()


observability_config = settings.observability_config()
app = FastAPI(title=settings.service_name, lifespan=lifespan)
app.state.kafka_producer = create_producer()
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
)
app.include_router(tickets.router)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}
