from __future__ import annotations

from collections.abc import Mapping
from contextlib import contextmanager
from typing import Any

import pytest
from opentelemetry.trace import SpanKind

from kafka_utils import (
    TraceAwareKafkaProducer,
    build_producer_headers,
    create_kafka_producer,
    start_consumer_span,
    start_producer_span,
    with_correlation_id,
    with_span_attributes,
    with_trace_context,
)
from kafka_utils import producer as producer_module


EVENT_ID = "6c98a5ce-8913-5597-9ad7-c617f71f0be3"


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
        client_id="reservation-service",
        producer_factory=factory,
    )

    assert producer is not None
    assert producer.raw_producer is producers[0]
    assert producers[0].kwargs["bootstrap_servers"] == "kafka:9092"
    assert producers[0].kwargs["client_id"] == "reservation-service"
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

    with start_consumer_span(message):
        pass

    assert extracted == [
        {
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
        }
    ]
    assert started[0]["name"] == "kafka.consume payment-approved"
    assert started[0]["context"] == "parent-context"


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
        TraceAwareKafkaProducer(raw_producer).send_and_wait(
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
        TraceAwareKafkaProducer(raw_producer).send_and_wait(
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


def test_trace_aware_producer_send_and_wait_failure_is_not_swallowed(monkeypatch) -> None:
    raw_producer = FailingProducer({})
    started: list[dict[str, object]] = []
    monkeypatch.setattr(producer_module.trace, "get_tracer", lambda name: FakeTracer(started))

    with pytest.raises(RuntimeError, match="kafka publish failed"):
        run_async(TraceAwareKafkaProducer(raw_producer).send_and_wait("payment-approved", {"eventId": EVENT_ID}))

    span = started[0]["span"]
    assert span.recorded_exceptions == ["kafka publish failed"]
    assert span.status.description == "kafka publish failed"


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


class FakeSpan:
    def __init__(self) -> None:
        self.recorded_exceptions: list[str] = []
        self.status: object | None = None

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
            yield span

        return span_context()


class FakeMessage:
    def __init__(self, *, topic: str, headers: list[tuple[str, bytes]]) -> None:
        self.topic = topic
        self.headers = headers
        self.partition = 0
        self.offset = 0


def run_async(awaitable):
    import asyncio

    return asyncio.run(awaitable)
