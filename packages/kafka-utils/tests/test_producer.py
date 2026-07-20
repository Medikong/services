from __future__ import annotations

from collections.abc import Mapping
from contextlib import contextmanager
from dataclasses import dataclass
import json
import logging
import re
from typing import Any

import pytest
from opentelemetry.trace import SpanContext, SpanKind, TraceFlags

from kafka_utils import (
    TraceAwareKafkaProducer,
    build_producer_headers,
    create_kafka_producer,
    headers_to_carrier,
    start_consumer_span,
    start_producer_span,
    with_correlation_id,
    with_span_attributes,
    with_trace_context,
)
from kafka_utils import producer as producer_module


EVENT_ID = "6c98a5ce-8913-5597-9ad7-c617f71f0be3"
TRACE_ID = "4f3b2c1a9d8e7f60123456789abcdef0"
SPAN_ID = "6f1a2b3c4d5e6f70"


def test_create_kafka_producer_returns_none_without_kafka_config() -> None:
    producer = create_kafka_producer("")

    assert producer is None


def test_create_kafka_producer_configures_aiokafka_producer() -> None:
    producers: list[FakeProducer] = []

    def factory(**kwargs: object) -> FakeProducer:
        producer = FakeProducer(kwargs)
        producers.append(producer)
        return producer

    producer = create_kafka_producer(
        "kafka:9092",
        client_id="order-service",
        producer_factory=factory,
    )

    assert producer is not None
    assert producer.raw_producer is producers[0]
    assert producers[0].kwargs["bootstrap_servers"] == "kafka:9092"
    assert producers[0].kwargs["client_id"] == "order-service"
    assert producers[0].kwargs["value_serializer"]({"eventId": EVENT_ID, "count": 1}) == (
        b'{"eventId":"6c98a5ce-8913-5597-9ad7-c617f71f0be3","count":1}'
    )


def test_create_kafka_producer_omits_empty_client_id() -> None:
    producer = create_kafka_producer(
        "kafka:9092",
        producer_factory=lambda **kwargs: FakeProducer(kwargs),
    )

    assert producer is not None
    assert "client_id" not in producer.raw_producer.kwargs


def test_build_producer_headers_uses_trace_and_correlation_headers(monkeypatch) -> None:
    def fake_inject(carrier: dict[str, str]) -> None:
        carrier["traceparent"] = "00-trace-span-01"
        carrier["tracestate"] = "vendor=value"

    monkeypatch.setattr(producer_module.propagate, "inject", fake_inject)

    headers = dict(build_producer_headers(correlation_id="req-1"))

    assert headers == {
        "traceparent": b"00-trace-span-01",
        "tracestate": b"vendor=value",
        "correlation_id": b"req-1",
    }


def test_build_producer_headers_uses_stored_trace_carrier(monkeypatch) -> None:
    monkeypatch.setattr(
        producer_module.propagate,
        "inject",
        lambda carrier: (_ for _ in ()).throw(AssertionError("unexpected current context injection")),
    )

    headers = dict(
        build_producer_headers(
            correlation_id="req-1",
            carrier={
                "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
                "tracestate": "vendor=value",
                "ignored": "value",
            },
        )
    )

    assert headers == {
        "traceparent": b"00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
        "tracestate": b"vendor=value",
        "correlation_id": b"req-1",
    }


def test_headers_to_carrier_ignores_malformed_utf8() -> None:
    assert headers_to_carrier(
        [("traceparent", b"\xff"), (b"\xff", b"value")]
    ) == {}


def test_start_consumer_span_extracts_trace_headers(monkeypatch) -> None:
    extracted: list[dict[str, str]] = []
    started: list[dict[str, object]] = []

    def fake_extract(carrier: dict[str, str]) -> object:
        extracted.append(carrier)
        return "parent-context"

    class FakeTracer:
        def start_as_current_span(self, name: str, **kwargs: object):
            started.append({"name": name, **kwargs})

            @contextmanager
            def span_context():
                yield object()

            return span_context()

    monkeypatch.setattr(producer_module.propagate, "extract", fake_extract)
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer())

    message = FakeMessage(
        topic="payment-approved",
        headers=[
            ("traceparent", b"00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01"),
            ("tracestate", b"vendor=value"),
            ("ignored", b"value"),
        ],
    )

    with start_consumer_span(message, service_name="order-service"):
        pass

    assert extracted == [
        {
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
        }
    ]
    assert started[0]["name"] == "kafka.consume payment-approved"
    assert started[0]["context"] == "parent-context"


def test_start_consumer_span_without_trace_headers_starts_root_span(monkeypatch) -> None:
    parent_is_valid: list[bool] = []

    class FakeTracer:
        def start_as_current_span(self, name: str, **kwargs: object):
            parent_is_valid.append(
                producer_module.trace.get_current_span(kwargs.get("context")).get_span_context().is_valid
            )

            @contextmanager
            def span_context():
                yield object()

            return span_context()

    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer())
    active_span = producer_module.trace.NonRecordingSpan(
        producer_module.trace.SpanContext(
            trace_id=0x4F3B2C1A9D8E7F60123456789ABCDEF0,
            span_id=0x6F1A2B3C4D5E6F70,
            is_remote=False,
            trace_flags=producer_module.trace.TraceFlags(1),
        )
    )
    message = FakeMessage(topic="payment-approved", headers=None)

    with producer_module.trace.use_span(active_span, end_on_exit=False):
        with start_consumer_span(message, service_name="order-service"):
            pass

    assert parent_is_valid == [False]


def test_start_producer_span_uses_stored_trace_carrier_as_parent(monkeypatch) -> None:
    extracted: list[dict[str, str]] = []
    started: list[dict[str, object]] = []

    def fake_extract(carrier: dict[str, str]) -> object:
        extracted.append(carrier)
        return "parent-context"

    class FakeTracer:
        def start_as_current_span(self, name: str, **kwargs: object):
            started.append({"name": name, **kwargs})

            @contextmanager
            def span_context():
                yield object()

            return span_context()

    monkeypatch.setattr(producer_module.propagate, "extract", fake_extract)
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer())

    with start_producer_span(
        "payment-approved",
        carrier={
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
            "correlation_id": "req-1",
        },
        attributes={"payment.event_type": "payment-approved"},
    ):
        pass

    assert extracted == [
        {
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
            "correlation_id": "req-1",
        }
    ]
    assert started[0]["name"] == "kafka.produce payment-approved"
    assert started[0]["context"] == "parent-context"
    assert started[0]["kind"] is SpanKind.PRODUCER
    assert started[0]["attributes"] == {
        "messaging.system": "kafka",
        "messaging.destination.name": "payment-approved",
        "messaging.operation": "publish",
        "correlation_id": "req-1",
        "payment.event_type": "payment-approved",
    }


def test_trace_aware_producer_send_and_wait_extracts_stored_parent_and_injects_producer_headers(
    monkeypatch,
) -> None:
    extracted: list[dict[str, str]] = []
    started: list[dict[str, object]] = []
    raw_producer = FakeProducer({})

    def fake_extract(carrier: dict[str, str]) -> object:
        extracted.append(carrier)
        return "parent-context"

    def fake_inject(carrier: dict[str, str]) -> None:
        carrier["traceparent"] = "00-child-trace-child-span-01"
        carrier["tracestate"] = "child=state"

    monkeypatch.setattr(producer_module.propagate, "extract", fake_extract)
    monkeypatch.setattr(producer_module.propagate, "inject", fake_inject)
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))

    result = run_async(
        TraceAwareKafkaProducer(raw_producer, service_name="payment-service").send_and_wait(
            "payment-approved",
            {"eventId": EVENT_ID},
            with_trace_context(
                {
                    "carrier": {
                        "traceparent": "00-parent-trace-parent-span-01",
                        "tracestate": "parent=state",
                    }
                }
            ),
            with_correlation_id("corr-1"),
            with_span_attributes({"payment.event_type": "payment-approved"}),
        )
    )

    assert result == "metadata"
    assert extracted == [
        {
            "traceparent": "00-parent-trace-parent-span-01",
            "tracestate": "parent=state",
        }
    ]
    assert started[0]["context"] == "parent-context"
    assert started[0]["kind"] is SpanKind.PRODUCER
    assert started[0]["attributes"]["payment.event_type"] == "payment-approved"
    assert started[0]["attributes"]["correlation_id"] == "corr-1"
    assert raw_producer.sent == [
        {
            "topic": "payment-approved",
            "value": {"eventId": EVENT_ID},
            "key": None,
            "partition": None,
            "timestamp_ms": None,
            "headers": [
                ("traceparent", b"00-child-trace-child-span-01"),
                ("tracestate", b"child=state"),
                ("correlation_id", b"corr-1"),
            ],
        }
    ]


def test_trace_aware_producer_merges_headers_with_wrapper_trace_priority(monkeypatch) -> None:
    raw_producer = FakeProducer({})
    monkeypatch.setattr(
        producer_module.propagate,
        "inject",
        lambda carrier: carrier.update({"traceparent": "00-wrapper-trace-wrapper-span-01"}),
    )
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer([]))

    run_async(
        TraceAwareKafkaProducer(raw_producer, service_name="payment-service").send_and_wait(
            "payment-approved",
            {"eventId": EVENT_ID},
            with_correlation_id("wrapper-correlation"),
            headers=[
                ("traceparent", b"caller-trace"),
                ("correlation_id", b"caller-correlation"),
                ("x-custom", b"keep-me"),
            ],
        )
    )

    assert raw_producer.sent[0]["headers"] == [
        ("x-custom", b"keep-me"),
        ("traceparent", b"00-wrapper-trace-wrapper-span-01"),
        ("correlation_id", b"wrapper-correlation"),
    ]


def test_trace_aware_producer_send_and_wait_failure_is_not_swallowed(monkeypatch, caplog) -> None:
    raw_producer = FailingProducer({})
    started: list[dict[str, object]] = []
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))
    caplog.set_level(logging.INFO)

    with pytest.raises(RuntimeError, match="kafka publish failed"):
        run_async(
            TraceAwareKafkaProducer(raw_producer, service_name="payment-service").send_and_wait(
                "payment-approved",
                {"token": "producer-secret", "card_number": "4111111111111111"},
                with_correlation_id("order-001"),
            )
        )

    span = started[0]["span"]
    assert span.recorded_exceptions == ["kafka publish failed"]
    assert span.status.description == "kafka publish failed"
    log = kafka_log(caplog.records, "kafka.message.publish")
    assert log["service.name"] == "payment-service"
    assert log["messaging.operation"] == "publish"
    assert log["messaging.destination.name"] == "payment-approved"
    assert log["correlation_id"] == "order-001"
    assert log["trace_id"] == TRACE_ID
    assert log["span_id"] == SPAN_ID
    assert log["outcome"] == "failure"
    assert log["failure.code"] == "RuntimeError"
    assert_safe_log(log, forbidden_values=("producer-secret", "4111111111111111"))


def test_trace_aware_producer_logs_correlated_publish_metadata(monkeypatch, caplog) -> None:
    raw_producer = MetadataProducer({})
    started: list[dict[str, object]] = []
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))
    caplog.set_level(logging.INFO)

    result = run_async(
        TraceAwareKafkaProducer(raw_producer, service_name="payment-service").send_and_wait(
            "payment.approved",
            {"value": "payload-secret", "card_token": "card-secret"},
            with_correlation_id("order-001"),
        )
    )

    assert result == FakeRecordMetadata(partition=2, offset=41)
    log = kafka_log(caplog.records, "kafka.message.publish")
    assert log == {
        "event": "kafka.message.publish",
        "service.name": "payment-service",
        "messaging.system": "kafka",
        "messaging.operation": "publish",
        "messaging.destination.name": "payment.approved",
        "messaging.kafka.partition": 2,
        "messaging.kafka.message.offset": 41,
        "correlation_id": "order-001",
        "trace_id": TRACE_ID,
        "span_id": SPAN_ID,
        "outcome": "success",
    }
    assert_safe_log(log, forbidden_values=("payload-secret", "card-secret"))


def test_consumer_span_logs_safe_correlated_processing_metadata(monkeypatch, caplog) -> None:
    started: list[dict[str, object]] = []
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))
    caplog.set_level(logging.INFO)
    message = FakeMessage(
        topic="payment.approved",
        partition=3,
        offset=42,
        headers=[("correlation_id", b"order-001")],
        value=b'{"token":"consumer-secret","card":"4111111111111111"}',
    )

    with start_consumer_span(message, service_name="order-service"):
        pass

    log = kafka_log(caplog.records, "kafka.message.process")
    assert log == {
        "event": "kafka.message.process",
        "service.name": "order-service",
        "messaging.system": "kafka",
        "messaging.operation": "process",
        "messaging.destination.name": "payment.approved",
        "messaging.kafka.partition": 3,
        "messaging.kafka.message.offset": 42,
        "correlation_id": "order-001",
        "trace_id": TRACE_ID,
        "span_id": SPAN_ID,
        "outcome": "success",
    }
    assert_safe_log(log, forbidden_values=("consumer-secret", "4111111111111111"))


def test_consumer_span_bounds_failure_code_and_omits_exception_message(monkeypatch, caplog) -> None:
    started: list[dict[str, object]] = []
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))
    caplog.set_level(logging.INFO)
    message = FakeMessage(
        topic="order.created",
        partition=0,
        offset=7,
        headers=[("correlation_id", b"order-002")],
    )

    with pytest.raises(VeryLongKafkaFailureCodeThatMustBeBoundedBeforeItReachesStructuredLogs):
        with start_consumer_span(message, service_name="payment-service"):
            raise VeryLongKafkaFailureCodeThatMustBeBoundedBeforeItReachesStructuredLogs(
                "token=consumer-secret card=4111111111111111"
            )

    log = kafka_log(caplog.records, "kafka.message.process")
    assert log["outcome"] == "failure"
    assert re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", log["failure.code"])
    assert_safe_log(log, forbidden_values=("consumer-secret", "4111111111111111"))


class FakeProducer:
    def __init__(self, kwargs: Mapping[str, Any]) -> None:
        self.kwargs = dict(kwargs)
        self.sent: list[dict[str, Any]] = []

    async def send(self, topic: str, **kwargs: object) -> str:
        self.sent.append({"topic": topic, **kwargs})
        return "future"

    async def send_and_wait(self, topic: str, **kwargs: object) -> str:
        self.sent.append({"topic": topic, **kwargs})
        return "metadata"


class FailingProducer(FakeProducer):
    async def send_and_wait(self, topic: str, **kwargs: object) -> str:
        raise RuntimeError("kafka publish failed")


@dataclass(frozen=True, slots=True)
class FakeRecordMetadata:
    partition: int
    offset: int


class MetadataProducer(FakeProducer):
    async def send_and_wait(self, topic: str, **kwargs: object) -> FakeRecordMetadata:
        self.sent.append({"topic": topic, **kwargs})
        return FakeRecordMetadata(partition=2, offset=41)


class FakeSpan:
    def __init__(self) -> None:
        self.recorded_exceptions: list[str] = []
        self.status: object | None = None
        self._context = SpanContext(
            trace_id=int(TRACE_ID, 16),
            span_id=int(SPAN_ID, 16),
            is_remote=False,
            trace_flags=TraceFlags(1),
        )

    def get_span_context(self) -> SpanContext:
        return self._context

    def record_exception(self, exc: Exception) -> None:
        self.recorded_exceptions.append(str(exc))

    def set_status(self, status: object) -> None:
        self.status = status


class FakeTracer:
    def __init__(self, started: list[dict[str, object]]) -> None:
        self._started = started

    def start_as_current_span(self, name: str, **kwargs: object):
        span = FakeSpan()
        self._started.append({"name": name, "span": span, **kwargs})

        @contextmanager
        def span_context():
            with producer_module.trace.use_span(span, end_on_exit=False):
                yield span

        return span_context()


class FakeMessage:
    def __init__(
        self,
        *,
        topic: str,
        headers: list[tuple[str, bytes]] | None,
        partition: int = 0,
        offset: int = 0,
        value: bytes | None = None,
    ) -> None:
        self.topic = topic
        self.headers = headers
        self.partition = partition
        self.offset = offset
        self.value = value


class VeryLongKafkaFailureCodeThatMustBeBoundedBeforeItReachesStructuredLogs(RuntimeError):
    pass


def kafka_log(records: list[logging.LogRecord], event: str) -> dict[str, Any]:
    for record in reversed(records):
        payload = json.loads(record.getMessage())
        if payload.get("event") == event:
            return payload
    raise AssertionError(f"missing {event} log")


def assert_safe_log(log: dict[str, Any], *, forbidden_values: tuple[str, ...]) -> None:
    serialized = json.dumps(log)
    for field in log:
        assert not {"payload", "value", "token", "card"}.intersection(field.lower().replace(".", "_").split("_"))
    for forbidden_value in forbidden_values:
        assert forbidden_value not in serialized


def run_async(awaitable):
    import asyncio

    return asyncio.run(awaitable)
