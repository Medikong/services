import asyncio
import signal

from app import models
from app.config import settings
from app.consumers.kafka_consumer import EventHandlers, consume_events
from app.database import SessionLocal, engine
from app.kafka import KafkaProducer, create_producer
from app.observability import configure_worker_observability
from app.services.ticket_service import PaymentApprovedEventHandler


_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS = 5.0


def kafka_event_handlers(kafka_producer: KafkaProducer) -> EventHandlers:
    return {settings.payment_approved_topic: PaymentApprovedEventHandler(SessionLocal, kafka_producer)}


def _install_signal_handlers(stop_event: asyncio.Event) -> None:
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, stop_event.set)
        except NotImplementedError:
            signal.signal(sig, lambda _signum, _frame: stop_event.set())


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


async def run_worker() -> None:
    """payment-approved consumer를 HTTP app과 별도 프로세스로 실행한다."""
    configure_worker_observability(settings.observability_config())
    models.Base.metadata.create_all(bind=engine)

    producer = create_producer()
    stop_event = asyncio.Event()
    _install_signal_handlers(stop_event)
    consumer_task: asyncio.Task[None] | None = None
    try:
        if producer is not None:
            await producer.start()
        consumer_task = asyncio.create_task(
            consume_events(
                stop_event,
                bootstrap_servers=settings.kafka_bootstrap_servers,
                group_id=settings.kafka_group_id,
                service_name=settings.service_name,
                handlers=kafka_event_handlers(producer),
            )
        )
        signal_task = asyncio.create_task(stop_event.wait())
        done, pending = await asyncio.wait(
            {consumer_task, signal_task},
            return_when=asyncio.FIRST_COMPLETED,
        )
        if signal_task in pending:
            signal_task.cancel()
            await asyncio.gather(signal_task, return_exceptions=True)
        if consumer_task in done:
            await consumer_task
    finally:
        await _stop_background_task(consumer_task, stop_event)
        if producer is not None:
            await producer.stop()
        engine.dispose()
