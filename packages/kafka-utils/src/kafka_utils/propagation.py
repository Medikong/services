from collections.abc import Mapping, Sequence
from typing import Final

from opentelemetry import propagate


TRACEPARENT_HEADER: Final = "traceparent"
TRACESTATE_HEADER: Final = "tracestate"
CORRELATION_ID_HEADER: Final = "correlation_id"
KafkaHeaders = list[tuple[str, bytes]]
_ALLOWED_HEADERS: Final = {
    TRACEPARENT_HEADER,
    TRACESTATE_HEADER,
    CORRELATION_ID_HEADER,
}


def build_producer_headers(
    *,
    correlation_id: str | None = None,
    carrier: Mapping[str, str] | None = None,
) -> KafkaHeaders:
    """Build the allowlisted trace and correlation headers for a publish."""
    resolved_carrier: dict[str, str] = {}
    if carrier is None:
        propagate.inject(resolved_carrier)
    else:
        resolved_carrier.update(string_carrier(carrier))

    resolved_correlation_id = string_value(correlation_id)
    if resolved_correlation_id:
        resolved_carrier[CORRELATION_ID_HEADER] = resolved_correlation_id

    return [
        (key, value.encode("utf-8"))
        for key, value in resolved_carrier.items()
        if key in _ALLOWED_HEADERS
    ]


def headers_to_carrier(
    headers: Sequence[tuple[str | bytes, bytes]] | None,
) -> dict[str, str]:
    """Decode only propagation headers from a Kafka message."""
    carrier: dict[str, str] = {}
    for key, value in headers or ():
        try:
            decoded_key = key.decode("utf-8") if isinstance(key, bytes) else key
            decoded_value = value.decode("utf-8")
        except UnicodeDecodeError:
            continue
        if decoded_key not in _ALLOWED_HEADERS:
            continue
        carrier[decoded_key] = decoded_value
    return carrier


def string_carrier(carrier: Mapping[str, str]) -> dict[str, str]:
    """Remove blank propagation keys and values."""
    resolved: dict[str, str] = {}
    for key, value in carrier.items():
        resolved_key = string_value(key)
        resolved_value = string_value(value)
        if resolved_key is None or resolved_value is None:
            continue
        resolved[resolved_key] = resolved_value
    return resolved


def string_value(value: str | None) -> str | None:
    """Return a stripped non-empty propagation value."""
    if value is None:
        return None
    text = value.strip()
    return text or None
