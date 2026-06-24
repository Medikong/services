import asyncio
import signal

from app import models
from app.config import settings
from app.database import SessionLocal, engine
from app.kafka import create_producer
from app.observability import configure_worker_observability
from app.schema_migrations import run_schema_migrations
from app.services.payment_events import run_payment_event_dispatcher


_BACKGROUND_TASK_SHUTDOWN_TIMEOUT_SECONDS = 5.0


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
    """payment outbox dispatcher를 HTTP app과 별도 프로세스로 실행한다."""
    configure_worker_observability(settings.observability_config())
    models.Base.metadata.create_all(bind=engine)
    run_schema_migrations(engine)

    producer = create_producer()
    if producer is None:
        engine.dispose()
        return

    stop_event = asyncio.Event()
    _install_signal_handlers(stop_event)
    dispatcher_task: asyncio.Task[None] | None = None
    try:
        await producer.start()
        dispatcher_task = asyncio.create_task(
            run_payment_event_dispatcher(
                stop_event,
                session_factory=SessionLocal,
                kafka_producer=producer,
                interval_seconds=settings.payment_event_dispatch_interval_seconds,
                batch_size=settings.payment_event_dispatch_batch_size,
                max_attempts=settings.payment_event_dispatch_max_attempts,
            )
        )
        signal_task = asyncio.create_task(stop_event.wait())
        done, pending = await asyncio.wait(
            {dispatcher_task, signal_task},
            return_when=asyncio.FIRST_COMPLETED,
        )
        if signal_task in pending:
            signal_task.cancel()
            await asyncio.gather(signal_task, return_exceptions=True)
        if dispatcher_task in done:
            await dispatcher_task
    finally:
        await _stop_background_task(dispatcher_task, stop_event)
        await producer.stop()
        engine.dispose()
