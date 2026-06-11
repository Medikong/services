from __future__ import annotations

from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import contextmanager
import json
from typing import Any

from aiokafka import AIOKafkaProducer
from opentelemetry import propagate, trace
from opentelemetry.trace import Span, SpanKind


TRACEPARENT_HEADER = "traceparent"
TRACESTATE_HEADER = "tracestate"
CORRELATION_ID_HEADER = "correlation_id"
KafkaHeaders = list[tuple[str, bytes]]


def create_kafka_producer(
    bootstrap_servers: str,
    *,
    client_id: str | None = None,
    producer_factory: Callable[..., AIOKafkaProducer] = AIOKafkaProducer,
) -> AIOKafkaProducer | None:
    if not bootstrap_servers:
        return None

    producer_kwargs: dict[str, object] = {
        "bootstrap_servers": bootstrap_servers,
        "value_serializer": _json_serializer,
    }
    if client_id is not None:
        producer_kwargs["client_id"] = client_id
    return producer_factory(**producer_kwargs)


def build_producer_headers(
    *,
    correlation_id: str | None = None,
    carrier: Mapping[str, str] | None = None,
) -> KafkaHeaders:
    resolved_carrier: dict[str, str] = {}
    if carrier is None:
        propagate.inject(resolved_carrier)
    else:
        resolved_carrier.update(_string_carrier(carrier))

    resolved_correlation_id = _string_value(correlation_id)
    if resolved_correlation_id:
        resolved_carrier[CORRELATION_ID_HEADER] = resolved_correlation_id

    return [(key, value.encode("utf-8")) for key, value in resolved_carrier.items() if key in _ALLOWED_HEADERS]


def headers_to_carrier(headers: Sequence[tuple[str | bytes, bytes]] | None) -> dict[str, str]:
    carrier: dict[str, str] = {}
    for key, value in headers or ():
        decoded_key = key.decode("utf-8") if isinstance(key, bytes) else key
        if decoded_key not in _ALLOWED_HEADERS:
            continue
        carrier[decoded_key] = value.decode("utf-8")
    return carrier


@contextmanager
def start_consumer_span(message: Any, *, name: str | None = None) -> Iterator[Span]:
    topic = str(getattr(message, "topic", "unknown"))
    carrier = headers_to_carrier(getattr(message, "headers", None))
    parent_context = propagate.extract(carrier)
    tracer = trace.get_tracer("kafka_utils.consumer")
    span_name = name or f"kafka.consume {topic}"

    with tracer.start_as_current_span(
        span_name,
        context=parent_context,
        kind=SpanKind.CONSUMER,
        attributes=kafka_message_attributes(message, carrier=carrier),
    ) as span:
        yield span


def kafka_message_attributes(message: Any, *, carrier: Mapping[str, str] | None = None) -> dict[str, str | int]:
    topic = str(getattr(message, "topic", "unknown"))
    attributes: dict[str, str | int] = {
        "messaging.system": "kafka",
        "messaging.destination.name": topic,
        "messaging.operation": "process",
    }
    partition = getattr(message, "partition", None)
    offset = getattr(message, "offset", None)
    if isinstance(partition, int):
        attributes["messaging.kafka.partition"] = partition
    if isinstance(offset, int):
        attributes["messaging.kafka.message.offset"] = offset

    correlation_id = (carrier or headers_to_carrier(getattr(message, "headers", None))).get(CORRELATION_ID_HEADER)
    if correlation_id:
        attributes[CORRELATION_ID_HEADER] = correlation_id
    return attributes


def _json_serializer(value: object) -> bytes:
    return json.dumps(value, separators=(",", ":")).encode("utf-8")


def _string_value(value: object) -> str | None:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _string_carrier(carrier: Mapping[str, str]) -> dict[str, str]:
    resolved: dict[str, str] = {}
    for key, value in carrier.items():
        resolved_key = _string_value(key)
        resolved_value = _string_value(value)
        if resolved_key is None or resolved_value is None:
            continue
        resolved[resolved_key] = resolved_value
    return resolved


_ALLOWED_HEADERS = {TRACEPARENT_HEADER, TRACESTATE_HEADER, CORRELATION_ID_HEADER}
