from __future__ import annotations

from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import contextmanager
from dataclasses import dataclass, field
import json
from typing import Any, TypeAlias

from aiokafka import AIOKafkaProducer
from opentelemetry import propagate, trace
from opentelemetry.trace import Span, SpanKind, Status, StatusCode


TRACEPARENT_HEADER = "traceparent"
TRACESTATE_HEADER = "tracestate"
CORRELATION_ID_HEADER = "correlation_id"
KafkaHeaders = list[tuple[str, bytes]]
SpanAttributeValue = str | bool | int | float
KafkaProducerOption: TypeAlias = Callable[["_KafkaProducerSendConfig"], None]


@dataclass
class _KafkaProducerSendConfig:
    trace_context: Mapping[str, object] | None = None
    trace_carrier: Mapping[str, str] | None = None
    correlation_id: str | None = None
    span_name: str | None = None
    span_attributes: dict[str, SpanAttributeValue] = field(default_factory=dict)


class TraceAwareKafkaProducer:
    def __init__(self, raw_producer: AIOKafkaProducer) -> None:
        self._producer = raw_producer

    @property
    def raw_producer(self) -> AIOKafkaProducer:
        return self._producer

    def __getattr__(self, name: str) -> Any:
        return getattr(self._producer, name)

    async def send(
        self,
        topic: str,
        value: object | None = None,
        *producer_options: KafkaProducerOption,
        key: bytes | None = None,
        partition: int | None = None,
        timestamp_ms: int | None = None,
        headers: Sequence[tuple[str | bytes, bytes]] | None = None,
    ) -> Any:
        send_options = _build_send_config(producer_options)
        with start_producer_span(
            topic,
            carrier=_trace_carrier_for_options(send_options),
            name=send_options.span_name,
            attributes=_span_attributes_for_options(send_options),
        ) as span:
            resolved_headers = _merge_headers(
                headers,
                build_producer_headers(correlation_id=send_options.correlation_id),
            )
            try:
                return await self._producer.send(
                    topic,
                    value=value,
                    key=key,
                    partition=partition,
                    timestamp_ms=timestamp_ms,
                    headers=resolved_headers,
                )
            except Exception as exc:
                _record_producer_exception(span, exc)
                raise

    async def send_and_wait(
        self,
        topic: str,
        value: object | None = None,
        *producer_options: KafkaProducerOption,
        key: bytes | None = None,
        partition: int | None = None,
        timestamp_ms: int | None = None,
        headers: Sequence[tuple[str | bytes, bytes]] | None = None,
    ) -> Any:
        send_options = _build_send_config(producer_options)
        with start_producer_span(
            topic,
            carrier=_trace_carrier_for_options(send_options),
            name=send_options.span_name,
            attributes=_span_attributes_for_options(send_options),
        ) as span:
            resolved_headers = _merge_headers(
                headers,
                build_producer_headers(correlation_id=send_options.correlation_id),
            )
            try:
                return await self._producer.send_and_wait(
                    topic,
                    value=value,
                    key=key,
                    partition=partition,
                    timestamp_ms=timestamp_ms,
                    headers=resolved_headers,
                )
            except Exception as exc:
                _record_producer_exception(span, exc)
                raise


def create_kafka_producer(
    bootstrap_servers: str,
    *,
    client_id: str | None = None,
    producer_factory: Callable[..., AIOKafkaProducer] = AIOKafkaProducer,
) -> TraceAwareKafkaProducer | None:
    if not bootstrap_servers:
        return None

    producer_kwargs: dict[str, object] = {
        "bootstrap_servers": bootstrap_servers,
        "value_serializer": _json_serializer,
    }
    if client_id is not None:
        producer_kwargs["client_id"] = client_id
    return TraceAwareKafkaProducer(producer_factory(**producer_kwargs))


def with_trace_context(trace_context: Mapping[str, object] | None) -> KafkaProducerOption:
    def apply(options: _KafkaProducerSendConfig) -> None:
        options.trace_context = trace_context

    return apply


def with_trace_carrier(trace_carrier: Mapping[str, str] | None) -> KafkaProducerOption:
    def apply(options: _KafkaProducerSendConfig) -> None:
        options.trace_carrier = trace_carrier

    return apply


def with_correlation_id(correlation_id: str | None) -> KafkaProducerOption:
    def apply(options: _KafkaProducerSendConfig) -> None:
        options.correlation_id = correlation_id

    return apply


def with_span_name(span_name: str | None) -> KafkaProducerOption:
    def apply(options: _KafkaProducerSendConfig) -> None:
        options.span_name = span_name

    return apply


def with_span_attributes(span_attributes: Mapping[str, SpanAttributeValue] | None) -> KafkaProducerOption:
    def apply(options: _KafkaProducerSendConfig) -> None:
        if span_attributes is not None:
            options.span_attributes.update(span_attributes)

    return apply


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


@contextmanager
def start_producer_span(
    topic: str,
    *,
    carrier: Mapping[str, str] | None = None,
    name: str | None = None,
    attributes: Mapping[str, SpanAttributeValue] | None = None,
) -> Iterator[Span]:
    resolved_carrier = _string_carrier(carrier or {})
    parent_context = propagate.extract(resolved_carrier) if resolved_carrier else None
    tracer = trace.get_tracer("kafka_utils.producer")
    span_attributes = kafka_producer_attributes(topic, carrier=resolved_carrier)
    if attributes is not None:
        span_attributes.update(attributes)

    span_kwargs: dict[str, object] = {
        "kind": SpanKind.PRODUCER,
        "attributes": span_attributes,
    }
    if parent_context is not None:
        span_kwargs["context"] = parent_context

    with tracer.start_as_current_span(name or f"kafka.produce {topic}", **span_kwargs) as span:
        yield span


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


def kafka_producer_attributes(topic: str, *, carrier: Mapping[str, str] | None = None) -> dict[str, str]:
    attributes = {
        "messaging.system": "kafka",
        "messaging.destination.name": topic,
        "messaging.operation": "publish",
    }
    correlation_id = (carrier or {}).get(CORRELATION_ID_HEADER)
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


def _build_send_config(producer_options: Sequence[KafkaProducerOption]) -> _KafkaProducerSendConfig:
    config = _KafkaProducerSendConfig()
    for option in producer_options:
        option(config)
    return config


def _trace_carrier_for_options(options: _KafkaProducerSendConfig) -> dict[str, str] | None:
    if options.trace_carrier is not None:
        return _string_carrier(options.trace_carrier)

    trace_context = options.trace_context
    if not isinstance(trace_context, Mapping):
        return None
    carrier = trace_context.get("carrier")
    if not isinstance(carrier, Mapping):
        return None

    resolved: dict[str, str] = {}
    for key, value in carrier.items():
        resolved_key = _string_value(key)
        resolved_value = _string_value(value)
        if resolved_key is None or resolved_value is None:
            continue
        resolved[resolved_key] = resolved_value
    return resolved or None


def _span_attributes_for_options(options: _KafkaProducerSendConfig) -> dict[str, SpanAttributeValue]:
    attributes = dict(options.span_attributes)
    correlation_id = _string_value(options.correlation_id)
    if correlation_id is not None:
        attributes[CORRELATION_ID_HEADER] = correlation_id
    return attributes


def _merge_headers(
    caller_headers: Sequence[tuple[str | bytes, bytes]] | None,
    producer_headers: KafkaHeaders,
) -> KafkaHeaders:
    producer_header_names = {key for key, _value in producer_headers}
    merged: KafkaHeaders = []
    for key, value in caller_headers or ():
        decoded_key = key.decode("utf-8") if isinstance(key, bytes) else key
        if decoded_key in producer_header_names:
            continue
        merged.append((decoded_key, value))
    merged.extend(producer_headers)
    return merged


def _record_producer_exception(span: Span, exc: Exception) -> None:
    span.record_exception(exc)
    span.set_status(Status(StatusCode.ERROR, str(exc)))


_ALLOWED_HEADERS = {TRACEPARENT_HEADER, TRACESTATE_HEADER, CORRELATION_ID_HEADER}
