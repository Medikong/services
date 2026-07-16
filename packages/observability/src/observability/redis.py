from __future__ import annotations

from threading import Lock
from typing import Any

from opentelemetry.instrumentation import redis as redis_instrumentation
from opentelemetry.instrumentation.redis import RedisInstrumentor
from opentelemetry.trace import Span, TracerProvider
from redis.connection import Connection

_QUERY_ATTRIBUTES = ("db.statement", "db.query.text")
_instrumentation_lock = Lock()
_redis_instrumented = False
_pipeline_metadata_sanitized = False


def instrument_redis(tracer_provider: TracerProvider | None = None) -> bool:
    """Instrument redis-py once without exporting keys, values, or arguments.

    Args:
        tracer_provider: Optional provider used by Redis spans. The global provider
            is used when omitted.

    Returns:
        True when instrumentation is installed by this call, otherwise False.
    """
    global _redis_instrumented

    with _instrumentation_lock:
        if _redis_instrumented:
            return False
        instrumentor = RedisInstrumentor()
        if instrumentor.is_instrumented_by_opentelemetry:
            raise RuntimeError(
                "Redis instrumentation is already active; disable automatic Redis "
                "instrumentation before calling instrument_redis"
            )
        _sanitize_pipeline_metadata()
        instrumentor.instrument(
            tracer_provider=tracer_provider,
            request_hook=_sanitize_request,
        )
        _redis_instrumented = True
        return True


def _sanitize_request(
    span: Span,
    _instance: Connection,
    args: list[Any],
    _kwargs: dict[str, Any],
) -> None:
    operation = _operation_name(args[0] if args else None)
    _replace_query_attributes(span, operation)


def _replace_query_attributes(span: Span, operation: str) -> None:
    if not span.is_recording():
        return
    for attribute in _QUERY_ATTRIBUTES:
        span.set_attribute(attribute, operation)


def _operation_name(value: object) -> str:
    if isinstance(value, bytes):
        return value.decode("ascii", errors="replace").upper()
    if isinstance(value, str):
        return value.upper()
    return "REDIS"


def _sanitize_pipeline_metadata() -> None:
    global _pipeline_metadata_sanitized

    if _pipeline_metadata_sanitized:
        return
    original = redis_instrumentation._build_span_meta_data_for_pipeline

    def sanitized(instance: Any) -> tuple[list[Any], str, str]:
        command_stack, _resource, span_name = original(instance)
        return command_stack, "PIPELINE", span_name

    redis_instrumentation._build_span_meta_data_for_pipeline = sanitized
    _pipeline_metadata_sanitized = True
