import asyncio
from contextlib import asynccontextmanager
from collections.abc import AsyncIterator

from fastapi import FastAPI
from server.operational import register_operational_handlers

from app import models
from app.config import settings
from app.consumers.kafka_consumer import consume_events
from app.database import engine
from app.observability import setup_request_logging
from app.routers import tickets

models.Base.metadata.create_all(bind=engine)


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    app.state.consumer_stop_event = asyncio.Event()
    app.state.consumer_task = asyncio.create_task(
        consume_events(app.state.consumer_stop_event)
    )
    try:
        yield
    finally:
        app.state.consumer_stop_event.set()
        app.state.consumer_task.cancel()


app = FastAPI(title=settings.service_name, lifespan=lifespan)
setup_request_logging(app, settings.service_name)
register_operational_handlers(
    app,
    service_name=settings.service_name,
    readiness_checks={},
    readiness_success_status="ok",
    readiness_failure_status="failed",
    include_readiness_checks=False,
)
app.include_router(tickets.router)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}
