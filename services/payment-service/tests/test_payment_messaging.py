from collections.abc import Sequence

import anyio
from kafka_utils import KafkaProducerOption, with_correlation_id, with_trace_context
import pytest

from app.kafka_workers import run_outbox_worker
from app.messaging import KafkaOutboxPublisher, OutboxWorkerRelay
from app.outbox import OutboxMessage, RelayPayload, TraceContextPayload


class RecordingProducer:
    def __init__(self) -> None:
        self.topic: str | None = None
        self.value: RelayPayload | None = None
        self.key: bytes | None = None

    async def start(self) -> None:
        return None

    async def stop(self) -> None:
        return None

    async def send_and_wait(
        self,
        topic: str,
        value: RelayPayload,
        *producer_options: KafkaProducerOption,
        key: bytes | None = None,
        partition: int | None = None,
        timestamp_ms: int | None = None,
        headers: Sequence[tuple[str | bytes, bytes]] | None = None,
    ) -> None:
        self.topic = topic
        self.value = value
        self.key = key


class WorkerComplete(BaseException):
    pass


class TransientWorkerError(RuntimeError):
    pass


class RestartRecordingPublisher(KafkaOutboxPublisher):
    def __init__(self) -> None:
        super().__init__(RecordingProducer())
        self.started = False
        self.stopped = False

    async def start(self) -> None:
        self.started = True

    async def stop(self) -> None:
        self.stopped = True


class FailingRelay:
    async def run(self) -> None:
        raise TransientWorkerError("transient worker failure")


class CompletingRelay:
    async def run(self) -> None:
        raise WorkerComplete


def test_kafka_publisher_uses_stored_envelope_and_trace_context(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    producer = RecordingProducer()
    publisher = KafkaOutboxPublisher(producer)
    captured_correlation_ids: list[str | None] = []
    captured_trace_contexts: list[TraceContextPayload | None] = []

    def correlation_option(value: str | None) -> KafkaProducerOption:
        captured_correlation_ids.append(value)
        return with_correlation_id(value)

    def trace_option(
        value: TraceContextPayload | None,
    ) -> KafkaProducerOption:
        captured_trace_contexts.append(value)
        return with_trace_context(value)

    monkeypatch.setattr("app.messaging.with_correlation_id", correlation_option)
    monkeypatch.setattr("app.messaging.with_trace_context", trace_option)
    payload: RelayPayload = {
        "eventId": "evt-payment-approved-payment-001",
        "correlationId": "order-001",
    }
    trace_context: TraceContextPayload = {
        "carrier": {"traceparent": "00-trace-span-01"}
    }
    message = OutboxMessage(
        event_id="evt-payment-approved-payment-001",
        topic="payment.approved",
        message_key="order-001",
        payload=payload,
        trace_context=trace_context,
    )

    # When
    anyio.run(publisher.publish, message)

    # Then
    assert producer.topic == "payment.approved"
    assert producer.value == payload
    assert producer.key == b"order-001"
    assert captured_correlation_ids == ["order-001"]
    assert captured_trace_contexts == [trace_context]


def test_outbox_worker_recovery_uses_fresh_kafka_client(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    publishers: list[RestartRecordingPublisher] = []

    def worker_factory() -> tuple[KafkaOutboxPublisher, OutboxWorkerRelay]:
        publisher = RestartRecordingPublisher()
        publishers.append(publisher)
        relay = FailingRelay() if len(publishers) == 1 else CompletingRelay()
        return publisher, relay

    monkeypatch.setattr("app.kafka_workers.WORKER_RETRY_DELAY_SECONDS", 0)

    # When
    with pytest.raises(WorkerComplete):
        anyio.run(run_outbox_worker, worker_factory)

    # Then
    assert len(publishers) == 2
    assert publishers[0] is not publishers[1]
    assert all(publisher.started for publisher in publishers)
    assert all(publisher.stopped for publisher in publishers)
