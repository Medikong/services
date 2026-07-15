import logging
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Final, override

import anyio
from aiokafka.errors import KafkaError
from sqlalchemy.exc import SQLAlchemyError

from app.messaging import (
    KafkaOutboxPublisher,
    OutboxWorkerFactory,
    PaymentConsumer,
    PaymentConsumerFactory,
)

WORKER_MAX_ATTEMPTS: Final[int] = 5
WORKER_RETRY_BASE_DELAY_SECONDS: Final[float] = 1.0
WORKER_RETRY_MAX_DELAY_SECONDS: Final[float] = 4.0
WORKER_STOP_TIMEOUT_SECONDS: Final[float] = 5.0
LOGGER: Final = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class WorkerRetriesExhausted(RuntimeError):
    worker_name: str
    attempts: int

    @override
    def __str__(self) -> str:
        return f"{self.worker_name} worker failed after {self.attempts} attempts"


async def run_outbox_worker(factory: OutboxWorkerFactory) -> None:
    async def run_once() -> None:
        publisher, relay = factory()
        try:
            await publisher.start()
            await relay.run()
        finally:
            await _stop_publisher(publisher)

    await _run_with_retries("outbox", run_once)


async def run_payment_consumer_worker(factory: PaymentConsumerFactory) -> None:
    async def run_once() -> None:
        consumer = factory()
        try:
            await consumer.start()
            await consumer.run()
        finally:
            await _stop_consumer(consumer)

    await _run_with_retries("payment consumer", run_once)


async def _run_with_retries(
    worker_name: str,
    run_once: Callable[[], Awaitable[None]],
) -> None:
    for attempt in range(1, WORKER_MAX_ATTEMPTS + 1):
        try:
            await run_once()
            return
        except (KafkaError, SQLAlchemyError, OSError, RuntimeError) as error:
            if attempt == WORKER_MAX_ATTEMPTS:
                raise WorkerRetriesExhausted(worker_name, attempt) from error
            delay: float = min(
                WORKER_RETRY_BASE_DELAY_SECONDS * 2.0 ** (attempt - 1),
                WORKER_RETRY_MAX_DELAY_SECONDS,
            )
            LOGGER.warning(
                "%s worker retrying after %s (attempt %d/%d, delay %.1fs)",
                worker_name,
                type(error).__name__,
                attempt,
                WORKER_MAX_ATTEMPTS,
                delay,
            )
            await anyio.sleep(delay)


async def _stop_publisher(publisher: KafkaOutboxPublisher) -> None:
    with anyio.move_on_after(WORKER_STOP_TIMEOUT_SECONDS, shield=True):
        try:
            await publisher.stop()
        except (KafkaError, OSError, RuntimeError) as error:
            LOGGER.warning("Kafka publisher stop failed with %s", type(error).__name__)


async def _stop_consumer(consumer: PaymentConsumer) -> None:
    with anyio.move_on_after(WORKER_STOP_TIMEOUT_SECONDS, shield=True):
        try:
            await consumer.stop()
        except (KafkaError, OSError, RuntimeError) as error:
            LOGGER.warning("Kafka consumer stop failed with %s", type(error).__name__)
