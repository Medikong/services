from __future__ import annotations

import json
import os
import time
from typing import Any
from urllib.parse import urlencode
from uuid import uuid4

from http_client import TIMEOUT_SECONDS, request_json, wait_ready
from log_assertions import (
    assert_log_graph,
    assert_low_cardinality_labels,
    assert_nonempty_ids,
    assert_sensitive_data_absent,
    assert_trace_link,
    safe_log_fields,
    unique_label_sets,
)


CATALOG_URL = os.environ.get("CATALOG_SERVICE_URL", "http://catalog-service:8081")
ORDER_URL = os.environ.get("ORDER_SERVICE_URL", "http://order-service:8082")
PAYMENT_URL = os.environ.get("PAYMENT_SERVICE_URL", "http://payment-service:8083")
LOKI_URL = os.environ.get("LOKI_URL", "http://loki:3100")
COMPOSE_PROJECT = os.environ.get("LOG_CORRELATION_COMPOSE_PROJECT", "dropmong-log-correlation")
USER_HEADERS = {"X-User-Id": "user-001", "X-User-Role": "CUSTOMER"}


def main() -> None:
    wait_ready(f"{LOKI_URL}/ready")
    drop_id, product_id, amount = select_product()
    happy = run_purchase(drop_id, product_id, amount, fail=False)
    failed = run_purchase(drop_id, product_id, amount, fail=True)

    happy_logs = wait_for_kafka_logs(happy["order_id"], expected=6)
    assert_log_graph(
        happy_logs,
        {
            ("order-service", "publish", "order.created"),
            ("payment-service", "process", "order.created"),
            ("payment-service", "publish", "payment.approved"),
            ("order-service", "process", "payment.approved"),
            ("order-service", "publish", "notification.requested"),
            ("notification-service", "process", "notification.requested"),
        },
        happy["order_id"],
    )
    http_logs = [
        assert_http_log("order-service", happy["order_request_id"]),
        assert_http_log("payment-service", happy["payment_request_id"]),
        assert_http_log("order-service", failed["order_request_id"]),
        assert_http_log("payment-service", failed["payment_request_id"]),
    ]
    assert_trace_link(http_logs[0], happy_logs, {"order.created"})
    assert_trace_link(
        http_logs[1],
        happy_logs,
        {"payment.approved", "notification.requested"},
    )

    failure_logs = wait_for_kafka_logs(failed["order_id"], expected=2)
    assert_log_graph(
        failure_logs,
        {
            ("payment-service", "publish", "payment.failed"),
            ("order-service", "process", "payment.failed"),
        },
        failed["order_id"],
    )
    failure_process = next(
        log
        for log in failure_logs
        if log.get("service.name") == "order-service"
        and log.get("messaging.destination.name") == "payment.failed"
    )
    assert failure_process.get("failure.code") == "payment_failed_event"
    assert_trace_link(http_logs[2], failure_logs, {"order.created"})
    assert_trace_link(http_logs[3], failure_logs, {"payment.failed"})
    assert_sensitive_data_absent(happy_logs + failure_logs)
    _, happy_labels = query_loki(
        "order-service|payment-service|notification-service",
        f'"correlation_id":"{happy["order_id"]}"',
    )
    _, failure_labels = query_loki(
        "order-service|payment-service|notification-service",
        f'"correlation_id":"{failed["order_id"]}"',
    )
    label_sets = unique_label_sets(happy_labels + failure_labels)
    assert_low_cardinality_labels(label_sets)

    print(
        json.dumps(
            {
                "result": "PASS",
                "happy_order_id": happy["order_id"],
                "failed_order_id": failed["order_id"],
                "happy_kafka_records": len(happy_logs),
                "failure_kafka_records": len(failure_logs),
                "http_records": http_logs,
                "happy_kafka_logs": safe_log_fields(happy_logs),
                "failure_kafka_logs": safe_log_fields(failure_logs),
                "failure_code": failure_process["failure.code"],
                "sensitive_fields": "absent",
                "runner": "compose-service-no-host-bind-mount",
                "loki_stream_labels": label_sets,
            },
            separators=(",", ":"),
        )
    )


def select_product() -> tuple[str, str, int]:
    payload = request_json("GET", f"{CATALOG_URL}/drops?limit=10", request_id=str(uuid4()))
    drop = payload["data"][0]
    product = drop["products"][0]
    return str(drop["id"]), str(product["id"]), int(product["price"])


def run_purchase(drop_id: str, product_id: str, amount: int, *, fail: bool) -> dict[str, str]:
    suffix = uuid4().hex
    order_request_id = str(uuid4())
    order_payload = request_json(
        "POST",
        f"{ORDER_URL}/orders",
        body={"dropId": drop_id, "productId": product_id, "quantity": 1},
        request_id=order_request_id,
        headers={**USER_HEADERS, "Idempotency-Key": f"log-order-{suffix}"},
        expected_status=201,
    )
    order_id = str(order_payload["data"]["id"])
    wait_for_kafka_logs(order_id, expected=2)
    payment_request_id = str(uuid4())
    path = "mock-failures" if fail else "mock-approvals"
    payment_body: dict[str, Any] = {
        "orderId": order_id,
        "amount": amount,
        "method": "MOCK_CARD",
    }
    if fail:
        payment_body["reason"] = "card_declined"
    request_json(
        "POST",
        f"{PAYMENT_URL}/payments/{path}",
        body=payment_body,
        request_id=payment_request_id,
        headers={**USER_HEADERS, "Idempotency-Key": f"log-payment-{suffix}"},
        expected_status=201,
    )
    wait_order_status(order_id, "PAYMENT_FAILED" if fail else "CONFIRMED")
    return {
        "order_id": order_id,
        "order_request_id": order_request_id,
        "payment_request_id": payment_request_id,
    }


def wait_order_status(order_id: str, expected: str) -> None:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        payload = request_json(
            "GET",
            f"{ORDER_URL}/orders/{order_id}",
            request_id=str(uuid4()),
            headers=USER_HEADERS,
        )
        if payload["data"]["status"] == expected:
            return
        time.sleep(1)
    raise AssertionError(f"order {order_id} did not reach {expected}")


def assert_http_log(service: str, request_id: str) -> dict[str, Any]:
    logs = wait_for_logs(service, f'"request_id":"{request_id}"', expected=1)
    log = logs[-1]
    assert log.get("request_id") == request_id
    assert log.get("correlation_id") == request_id
    assert_nonempty_ids(log)
    return {
        "service.name": service,
        "request_id": log["request_id"],
        "correlation_id": log["correlation_id"],
        "trace_id": log["trace_id"],
        "span_id": log["span_id"],
    }


def wait_for_kafka_logs(correlation_id: str, *, expected: int) -> list[dict[str, Any]]:
    return wait_for_logs(
        "order-service|payment-service|notification-service",
        f'"correlation_id":"{correlation_id}"',
        expected=expected,
    )


def wait_for_logs(service_pattern: str, text: str, *, expected: int) -> list[dict[str, Any]]:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        logs, _ = query_loki(service_pattern, text)
        if len(logs) >= expected:
            return logs
        time.sleep(2)
    raise AssertionError(f"Loki returned fewer than {expected} records for {text}")


def query_loki(
    service_pattern: str,
    text: str,
) -> tuple[list[dict[str, Any]], list[dict[str, str]]]:
    now = time.time_ns()
    query = (
        f'{{compose_project="{COMPOSE_PROJECT}",service=~"{service_pattern}"}} '
        f'|= {json.dumps(text)}'
    )
    params = urlencode(
        {
            "query": query,
            "start": str(now - 10 * 60 * 1_000_000_000),
            "end": str(now + 10 * 1_000_000_000),
            "limit": "1000",
            "direction": "forward",
        }
    )
    payload = request_json("GET", f"{LOKI_URL}/loki/api/v1/query_range?{params}")
    logs: list[dict[str, Any]] = []
    seen_logs: set[str] = set()
    label_sets: list[dict[str, str]] = []
    for stream in payload.get("data", {}).get("result", []):
        labels = stream.get("stream", {})
        if isinstance(labels, dict):
            label_sets.append({str(key): str(value) for key, value in labels.items()})
        for _, line in stream.get("values", []):
            try:
                decoded = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(decoded, dict):
                fingerprint = json.dumps(decoded, sort_keys=True, separators=(",", ":"))
                assert fingerprint not in seen_logs, "duplicate Loki record returned"
                seen_logs.add(fingerprint)
                logs.append(decoded)
    return logs, label_sets


if __name__ == "__main__":
    main()
