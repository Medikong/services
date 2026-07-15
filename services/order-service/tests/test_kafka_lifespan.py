from typing import cast

import anyio
from fastapi import FastAPI
from fastapi.testclient import TestClient
import pytest

from app.db import AppResources, lifespan_for
from app.kafka_workers import WorkerRetriesExhausted, run_outbox_worker
from app.messaging import KafkaOutboxPublisher, KafkaRuntime
from app.outbox import OutboxMessage, OutboxRelay
from app.store import OrderStore


class UnavailablePublisher:
    def __init__(self) -> None:
        self.stopped = False

    async def start(self) -> None:
        raise OSError("broker unavailable")

    async def stop(self) -> None:
        self.stopped = True

    async def publish(self, message: OutboxMessage) -> None:
        raise OSError("broker unavailable")


class UnusedRelay:
    async def run(self) -> None:
        raise AssertionError("relay must wait for the publisher to start")


class WorkerComplete(BaseException):
    pass


class TransientWorkerError(RuntimeError):
    pass


class RestartRecordingPublisher:
    def __init__(self) -> None:
        self.started = False
        self.stopped = False

    async def start(self) -> None:
        self.started = True

    async def stop(self) -> None:
        self.stopped = True

    async def publish(self, message: OutboxMessage) -> None:
        return None


class FailingRelay:
    async def run(self) -> None:
        raise TransientWorkerError("transient worker failure")


class CompletingRelay:
    async def run(self) -> None:
        raise WorkerComplete


def test_http_lifespan_starts_when_kafka_is_unavailable() -> None:
    # Given
    publishers: list[UnavailablePublisher] = []

    def unavailable_outbox_factory() -> tuple[KafkaOutboxPublisher, OutboxRelay]:
        publisher = UnavailablePublisher()
        publishers.append(publisher)
        return (
            cast(KafkaOutboxPublisher, publisher),
            cast(OutboxRelay, UnusedRelay()),
        )

    runtime = KafkaRuntime(
        payment_consumer_factory=None,
        outbox_worker_factory=unavailable_outbox_factory,
    )
    resources = AppResources(repository=OrderStore(), kafka_runtime=runtime)
    app = FastAPI(lifespan=lifespan_for(resources))

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok"}

    # When
    with TestClient(app) as client:
        response = client.get("/healthz")

    # Then
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}
    assert publishers
    assert all(publisher.stopped for publisher in publishers)


def test_outbox_worker_recovery_uses_a_fresh_kafka_client(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    publishers: list[RestartRecordingPublisher] = []

    def worker_factory() -> tuple[KafkaOutboxPublisher, OutboxRelay]:
        publisher = RestartRecordingPublisher()
        publishers.append(publisher)
        relay = FailingRelay() if len(publishers) == 1 else CompletingRelay()
        return (
            cast(KafkaOutboxPublisher, publisher),
            cast(OutboxRelay, relay),
        )

    monkeypatch.setattr("app.kafka_workers.WORKER_RETRY_BASE_DELAY_SECONDS", 0)

    # When
    with pytest.raises(WorkerComplete):
        anyio.run(run_outbox_worker, worker_factory)

    # Then
    assert len(publishers) == 2
    assert publishers[0] is not publishers[1]
    assert all(publisher.started for publisher in publishers)
    assert all(publisher.stopped for publisher in publishers)


def test_outbox_worker_raises_after_bounded_exponential_retries(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    publishers: list[RestartRecordingPublisher] = []
    delays: list[float] = []

    def worker_factory() -> tuple[KafkaOutboxPublisher, OutboxRelay]:
        publisher = RestartRecordingPublisher()
        publishers.append(publisher)
        return (
            cast(KafkaOutboxPublisher, publisher),
            cast(OutboxRelay, FailingRelay()),
        )

    async def record_delay(delay: float) -> None:
        delays.append(delay)

    monkeypatch.setattr("app.kafka_workers.anyio.sleep", record_delay)

    # When
    with pytest.raises(WorkerRetriesExhausted) as raised:
        anyio.run(run_outbox_worker, worker_factory)

    # Then
    assert raised.value.worker_name == "outbox"
    assert raised.value.attempts == 5
    assert len(publishers) == 5
    assert delays == [1.0, 2.0, 4.0, 4.0]
    assert all(publisher.stopped for publisher in publishers)
