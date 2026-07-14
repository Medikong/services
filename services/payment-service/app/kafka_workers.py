import logging
from typing import Final, Protocol

import anyio
from aiokafka.errors import KafkaError
from sqlalchemy.exc import SQLAlchemyError

from app.messaging import (
    KafkaOutboxPublisher,
    OrderCreatedConsumerFactory,
    OutboxWorkerFactory,
)
from app.refund_messaging import RefundRequestedConsumerFactory
from app.refund_worker import RefundWorker

WORKER_RETRY_DELAY_SECONDS: Final = 1.0
WORKER_STOP_TIMEOUT_SECONDS: Final = 5.0
LOGGER: Final = logging.getLogger(__name__)


class StoppableConsumer(Protocol):
    async def stop(self) -> None: ...


async def run_outbox_worker(factory: OutboxWorkerFactory) -> None:
    while True:
        publisher, relay = factory()
        try:
            await publisher.start()
            await relay.run()
        except (KafkaError, SQLAlchemyError, OSError, RuntimeError) as error:
            LOGGER.warning("outbox worker restarting after %s", type(error).__name__)
        finally:
            await _stop_publisher(publisher)
        await anyio.sleep(WORKER_RETRY_DELAY_SECONDS)


async def run_order_created_consumer_worker(
    factory: OrderCreatedConsumerFactory,
) -> None:
    while True:
        consumer = factory()
        try:
            await consumer.start()
            await consumer.run()
        except (KafkaError, SQLAlchemyError, OSError, RuntimeError) as error:
            LOGGER.warning(
                "order.created consumer restarting after %s",
                type(error).__name__,
            )
        finally:
            await _stop_consumer(consumer)
        await anyio.sleep(WORKER_RETRY_DELAY_SECONDS)


async def run_refund_requested_consumer_worker(
    factory: RefundRequestedConsumerFactory,
) -> None:
    while True:
        consumer = factory()
        try:
            await consumer.start()
            await consumer.run()
        except (KafkaError, SQLAlchemyError, OSError, RuntimeError) as error:
            LOGGER.warning(
                "refund.requested consumer restarting after %s",
                type(error).__name__,
            )
        finally:
            await _stop_consumer(consumer)
        await anyio.sleep(WORKER_RETRY_DELAY_SECONDS)


async def run_refund_worker(worker: RefundWorker) -> None:
    while True:
        try:
            await worker.run()
        except (SQLAlchemyError, OSError, RuntimeError) as error:
            LOGGER.warning(
                "refund worker restarting after %s",
                type(error).__name__,
            )
        await anyio.sleep(WORKER_RETRY_DELAY_SECONDS)


async def _stop_publisher(publisher: KafkaOutboxPublisher) -> None:
    with anyio.move_on_after(WORKER_STOP_TIMEOUT_SECONDS, shield=True):
        try:
            await publisher.stop()
        except (KafkaError, OSError, RuntimeError) as error:
            LOGGER.warning("Kafka publisher stop failed with %s", type(error).__name__)


async def _stop_consumer(consumer: StoppableConsumer) -> None:
    with anyio.move_on_after(WORKER_STOP_TIMEOUT_SECONDS, shield=True):
        try:
            await consumer.stop()
        except (KafkaError, OSError, RuntimeError) as error:
            LOGGER.warning("Kafka consumer stop failed with %s", type(error).__name__)
