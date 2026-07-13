from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest


SMOKE_DIR = Path(__file__).resolve().parents[2] / "log-correlation"
sys.path.insert(0, str(SMOKE_DIR))

import smoke  # noqa: E402


def test_log_correlation_image_contains_smoke_dependencies() -> None:
    dockerfile = (SMOKE_DIR / "Dockerfile").read_text(encoding="utf-8")
    assert "COPY smoke.py /app/smoke.py" in dockerfile
    assert "COPY http_client.py /app/http_client.py" in dockerfile
    assert "COPY log_assertions.py /app/log_assertions.py" in dockerfile


def test_query_loki_deduplicates_identical_records(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    log = {
        "event": "kafka.message.publish",
        "service.name": "order-service",
        "messaging.system": "kafka",
        "messaging.operation": "publish",
        "messaging.destination.name": "order.created",
        "correlation_id": "order-123",
        "trace_id": "a" * 32,
        "span_id": "b" * 16,
        "outcome": "success",
    }
    payload = {
        "data": {
            "result": [
                {
                    "stream": {"service": "order-service"},
                    "values": [["1", json.dumps(log)], ["2", json.dumps(log)]],
                }
            ]
        }
    }
    monkeypatch.setattr(smoke, "request_json", lambda *_args, **_kwargs: payload)

    logs, _labels = smoke.query_loki("order-service", '"correlation_id":"order-123"')

    assert logs == [log]


def test_sensitive_data_check_accepts_allowlisted_kafka_metadata() -> None:
    smoke.assert_sensitive_data_absent(
        [
            {
                "event": "kafka.message.process",
                "service.name": "notification-service",
                "messaging.system": "kafka",
                "messaging.operation": "process",
                "messaging.destination.name": "notification.requested",
                "messaging.kafka.partition": 0,
                "messaging.kafka.message.offset": 42,
                "correlation_id": "order-123",
                "trace_id": "a" * 32,
                "span_id": "b" * 16,
                "outcome": "success",
            }
        ]
    )


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("headers", {"authorization": "Bearer secret"}),
        ("kafka_key", "customer@example.com"),
        ("correlation_id", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature"),
        ("correlation_id", "customer@example.com"),
        ("correlation_id", "4111111111111111"),
    ],
)
def test_sensitive_data_check_rejects_unapproved_fields_and_values(
    field: str,
    value: str | dict[str, str],
) -> None:
    log = {
        "event": "kafka.message.publish",
        "service.name": "payment-service",
        "messaging.system": "kafka",
        "messaging.operation": "publish",
        "messaging.destination.name": "payment.approved",
        "correlation_id": "order-123",
        "trace_id": "a" * 32,
        "span_id": "b" * 16,
        "outcome": "success",
        field: value,
    }

    with pytest.raises(AssertionError):
        smoke.assert_sensitive_data_absent([log])


@pytest.mark.parametrize(
    ("fail", "payment_path", "terminal_status"),
    [
        (False, "mock-approvals", "CONFIRMED"),
        (True, "mock-failures", "PAYMENT_FAILED"),
    ],
)
def test_run_purchase_waits_for_payment_consumer_before_payment(
    monkeypatch: pytest.MonkeyPatch,
    *,
    fail: bool,
    payment_path: str,
    terminal_status: str,
) -> None:
    events: list[str] = []

    def fake_request_json(
        method: str,
        url: str,
        *,
        body: dict[str, str | int] | None = None,
        request_id: str | None = None,
        headers: dict[str, str] | None = None,
        expected_status: int = 200,
    ) -> dict[str, dict[str, str]]:
        del method, body, request_id, headers, expected_status
        if url == f"{smoke.ORDER_URL}/orders":
            events.append("order-created")
            return {"data": {"id": "order-123"}}
        events.append(f"payment-posted:{url.rsplit('/', 1)[-1]}")
        return {}

    def fake_wait_order_status(order_id: str, expected: str) -> None:
        assert order_id == "order-123"
        events.append(f"wait-order:{expected}")

    def fake_wait_for_kafka_logs(
        order_id: str,
        *,
        expected: int,
    ) -> list[dict[str, str]]:
        events.append(f"wait-kafka:{order_id}:{expected}")
        return []

    monkeypatch.setattr(smoke, "request_json", fake_request_json)
    monkeypatch.setattr(smoke, "wait_order_status", fake_wait_order_status)
    monkeypatch.setattr(smoke, "wait_for_kafka_logs", fake_wait_for_kafka_logs)

    smoke.run_purchase("drop-1", "product-1", 1000, fail=fail)

    assert events == [
        "order-created",
        "wait-kafka:order-123:2",
        f"payment-posted:{payment_path}",
        f"wait-order:{terminal_status}",
    ]
