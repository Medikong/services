from __future__ import annotations

import json
import re
from typing import Any, Final


KAFKA_LOG_ALLOWED_FIELDS: Final[frozenset[str]] = frozenset(
    {
        "event",
        "service.name",
        "messaging.system",
        "messaging.operation",
        "messaging.destination.name",
        "messaging.kafka.partition",
        "messaging.kafka.message.offset",
        "correlation_id",
        "trace_id",
        "span_id",
        "outcome",
        "failure.code",
    }
)
SENSITIVE_VALUE_PATTERNS: Final[tuple[re.Pattern[str], ...]] = (
    re.compile(r"(?i)\bbearer\s+[a-z0-9._~+/=-]+"),
    re.compile(r"\beyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b"),
    re.compile(r"(?i)\b[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+\b"),
    re.compile(r"\b\d{13,19}\b"),
    re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
)


def safe_log_fields(logs: list[dict[str, Any]]) -> list[dict[str, Any]]:
    fields = (
        "service.name",
        "messaging.operation",
        "messaging.destination.name",
        "messaging.kafka.partition",
        "messaging.kafka.message.offset",
        "correlation_id",
        "trace_id",
        "span_id",
        "outcome",
        "failure.code",
    )
    return [{field: log[field] for field in fields if field in log} for log in logs]


def assert_trace_link(
    http_log: dict[str, Any],
    kafka_logs: list[dict[str, Any]],
    topics: set[str],
) -> None:
    linked = [
        log
        for log in kafka_logs
        if log.get("messaging.destination.name") in topics
    ]
    assert linked, f"no Kafka logs found for topics {sorted(topics)}"
    assert {log.get("trace_id") for log in linked} == {http_log["trace_id"]}


def unique_label_sets(label_sets: list[dict[str, str]]) -> list[dict[str, str]]:
    unique = {tuple(sorted(labels.items())) for labels in label_sets}
    return [dict(items) for items in sorted(unique)]


def assert_low_cardinality_labels(label_sets: list[dict[str, str]]) -> None:
    assert label_sets, "Loki query returned no stream labels"
    forbidden = {"request_id", "correlation_id", "trace_id", "span_id"}
    for labels in label_sets:
        unexpected = forbidden & labels.keys()
        assert not unexpected, f"high-cardinality Loki labels: {sorted(unexpected)}"


def assert_log_graph(
    logs: list[dict[str, Any]],
    expected: set[tuple[str, str, str]],
    correlation_id: str,
) -> None:
    actual = {
        (
            str(log.get("service.name")),
            str(log.get("messaging.operation")),
            str(log.get("messaging.destination.name")),
        )
        for log in logs
    }
    missing = expected - actual
    assert not missing, f"missing Kafka log pairs: {sorted(missing)}"
    for log in logs:
        assert log.get("correlation_id") == correlation_id
        assert_nonempty_ids(log)
        assert log.get("outcome") == "success"


def assert_nonempty_ids(log: dict[str, Any]) -> None:
    assert isinstance(log.get("trace_id"), str) and log["trace_id"]
    assert isinstance(log.get("span_id"), str) and log["span_id"]


def assert_sensitive_data_absent(logs: list[dict[str, Any]]) -> None:
    for log in logs:
        unexpected_fields = set(log) - KAFKA_LOG_ALLOWED_FIELDS
        assert not unexpected_fields, f"unapproved Kafka log fields: {sorted(unexpected_fields)}"
        serialized = json.dumps(log, ensure_ascii=False, separators=(",", ":"))
        for pattern in SENSITIVE_VALUE_PATTERNS:
            assert pattern.search(serialized) is None, "sensitive value found in Kafka log"
