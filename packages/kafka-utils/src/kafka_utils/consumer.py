from collections.abc import Iterator, Mapping, Sequence
from contextlib import contextmanager
import sys
from typing import Protocol

from opentelemetry import propagate, trace
from opentelemetry.trace import Span, SpanKind, Status, StatusCode

from kafka_utils.logging import KafkaLogContext, log_kafka_operation
from kafka_utils.propagation import CORRELATION_ID_HEADER, headers_to_carrier


class KafkaMessage(Protocol):
    """Message metadata required for trace and log correlation."""

    topic: str
    partition: int
    offset: int
    headers: Sequence[tuple[str | bytes, bytes]] | None


@contextmanager
def start_consumer_span(
    message: KafkaMessage,
    *,
    service_name: str,
    name: str | None = None,
    failure_code: str | None = None,
) -> Iterator[Span]:
    """Trace and log one Kafka message processing operation."""
    carrier = headers_to_carrier(message.headers)
    parent_context = propagate.extract(carrier)
    tracer = trace.get_tracer("kafka_utils.consumer")
    span_name = name or f"kafka.consume {message.topic}"

    with tracer.start_as_current_span(
        span_name,
        context=parent_context,
        kind=SpanKind.CONSUMER,
        attributes=kafka_message_attributes(message, carrier=carrier),
    ) as span:
        log_context = KafkaLogContext(
            service_name=service_name,
            operation="process",
            topic=message.topic,
            partition=message.partition,
            offset=message.offset,
            correlation_id=carrier.get(CORRELATION_ID_HEADER, ""),
            span=span,
        )
        try:
            yield span
        finally:
            failure = sys.exception()
            if failure is None:
                log_kafka_operation(log_context, "success", failure_code)
            else:
                _record_consumer_exception(span, failure)
                log_kafka_operation(
                    log_context,
                    "failure",
                    type(failure).__name__,
                )


def kafka_message_attributes(
    message: KafkaMessage,
    *,
    carrier: Mapping[str, str] | None = None,
) -> dict[str, str | int]:
    """Return safe semantic-convention attributes for a consumed message."""
    resolved_carrier = carrier or headers_to_carrier(message.headers)
    attributes: dict[str, str | int] = {
        "messaging.system": "kafka",
        "messaging.destination.name": message.topic,
        "messaging.operation": "process",
        "messaging.kafka.partition": message.partition,
        "messaging.kafka.message.offset": message.offset,
    }
    correlation_id = resolved_carrier.get(CORRELATION_ID_HEADER)
    if correlation_id:
        attributes[CORRELATION_ID_HEADER] = correlation_id
    return attributes


def _record_consumer_exception(span: Span, failure: BaseException) -> None:
    span.record_exception(failure)
    span.set_status(Status(StatusCode.ERROR, type(failure).__name__))
