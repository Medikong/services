import asyncio
from contextlib import asynccontextmanager
from collections.abc import AsyncIterator

from fastapi import FastAPI
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
app.include_router(tickets.router)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.get("/readyz")
def readyz() -> dict[str, str]:
    return {"status": "ok", "service": settings.service_name}


@app.get("/metrics")
def metrics() -> dict[str, str]:
    return {"status": "ok"}
