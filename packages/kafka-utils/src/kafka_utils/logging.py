from dataclasses import dataclass
import json
import logging
from string import ascii_letters, digits
from typing import Final, Literal, assert_never

from opentelemetry.trace import Span


KafkaOperation = Literal["process", "publish"]
KafkaOutcome = Literal["failure", "success"]
MAX_FAILURE_CODE_LENGTH: Final = 64
_FAILURE_CODE_CHARACTERS: Final = frozenset(ascii_letters + digits + "._-")


@dataclass(frozen=True, slots=True)
class KafkaLogContext:
    """Safe fields shared by Kafka producer and consumer completion logs."""

    service_name: str
    operation: KafkaOperation
    topic: str
    correlation_id: str
    span: Span
    partition: int | None = None
    offset: int | None = None


def log_kafka_operation(
    context: KafkaLogContext,
    outcome: KafkaOutcome,
    failure_code: str | None = None,
) -> None:
    """Emit one allowlisted Kafka completion log without message data."""
    get_span_context = getattr(context.span, "get_span_context", None)
    span_context = get_span_context() if callable(get_span_context) else None
    trace_id = format(span_context.trace_id, "032x") if span_context is not None and span_context.is_valid else ""
    span_id = format(span_context.span_id, "016x") if span_context is not None and span_context.is_valid else ""
    fields: dict[str, str | int] = {
        "service.name": context.service_name,
        "messaging.system": "kafka",
        "messaging.operation": context.operation,
        "messaging.destination.name": context.topic,
        "correlation_id": context.correlation_id,
        "trace_id": trace_id,
        "span_id": span_id,
        "outcome": outcome,
    }
    if context.partition is not None:
        fields["messaging.kafka.partition"] = context.partition
    if context.offset is not None:
        fields["messaging.kafka.message.offset"] = context.offset
    safe_failure_code = bounded_failure_code(failure_code)
    if safe_failure_code is not None:
        fields["failure.code"] = safe_failure_code

    event = f"kafka.message.{context.operation}"
    logger = logging.getLogger(context.service_name)
    message = json.dumps({"event": event, **fields}, separators=(",", ":"))
    match outcome:
        case "success":
            logger.info(message)
        case "failure":
            logger.error(message)
        case unreachable:
            assert_never(unreachable)


def bounded_failure_code(failure_code: str | None) -> str | None:
    """Normalize a low-cardinality failure code to a bounded safe value."""
    if failure_code is None:
        return None
    normalized = "".join(
        character if character in _FAILURE_CODE_CHARACTERS else "_"
        for character in failure_code.strip()
    )
    return normalized[:MAX_FAILURE_CODE_LENGTH] or None
