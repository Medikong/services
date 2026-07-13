from __future__ import annotations

import json
import os
import time
from typing import Any
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from uuid import uuid4


CATALOG_URL = os.environ.get("CATALOG_SERVICE_URL", "http://catalog-service:8081")
ORDER_URL = os.environ.get("ORDER_SERVICE_URL", "http://order-service:8082")
PAYMENT_URL = os.environ.get("PAYMENT_SERVICE_URL", "http://payment-service:8083")
LOKI_URL = os.environ.get("LOKI_URL", "http://loki:3100")
TIMEOUT_SECONDS = int(os.environ.get("LOG_CORRELATION_TIMEOUT_SECONDS", "120"))
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
    assert_http_log("order-service", happy["order_request_id"])
    assert_http_log("payment-service", happy["payment_request_id"])

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
    assert_sensitive_data_absent(happy_logs + failure_logs)

    print(
        json.dumps(
            {
                "result": "PASS",
                "happy_order_id": happy["order_id"],
                "failed_order_id": failed["order_id"],
                "happy_kafka_records": len(happy_logs),
                "failure_kafka_records": len(failure_logs),
                "http_records": 4,
                "sensitive_fields": "absent",
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
    expected_order_status = "PAYMENT_FAILED" if fail else "CONFIRMED"
    wait_order_status(order_id, expected_order_status)
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


def assert_http_log(service: str, request_id: str) -> None:
    logs = wait_for_logs(service, f'"request_id":"{request_id}"', expected=1)
    log = logs[-1]
    assert log.get("request_id") == request_id
    assert log.get("correlation_id") == request_id
    assert_nonempty_ids(log)


def wait_for_kafka_logs(correlation_id: str, *, expected: int) -> list[dict[str, Any]]:
    return wait_for_logs(
        "order-service|payment-service|notification-service",
        f'"correlation_id":"{correlation_id}"',
        expected=expected,
    )


def wait_for_logs(service_pattern: str, text: str, *, expected: int) -> list[dict[str, Any]]:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        logs = query_loki(service_pattern, text)
        if len(logs) >= expected:
            return logs
        time.sleep(2)
    raise AssertionError(f"Loki returned fewer than {expected} records for {text}")


def query_loki(service_pattern: str, text: str) -> list[dict[str, Any]]:
    now = time.time_ns()
    query = f'{{service=~"{service_pattern}"}} |= {json.dumps(text)}'
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
    for stream in payload.get("data", {}).get("result", []):
        for _, line in stream.get("values", []):
            try:
                decoded = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(decoded, dict):
                logs.append(decoded)
    return logs


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
    forbidden_fields = {"authorization", "token", "card", "payload", "value", "kafka_value"}
    serialized = json.dumps(logs).lower()
    assert "4111111111111111" not in serialized
    for log in logs:
        for key in log:
            normalized = key.lower().replace(".", "_").split("_")
            assert forbidden_fields.isdisjoint(normalized), f"sensitive log field: {key}"


def wait_ready(url: str) -> None:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        try:
            with urlopen(url, timeout=5) as response:
                if response.status == 200:
                    return
        except OSError:
            pass
        time.sleep(2)
    raise AssertionError(f"endpoint did not become ready: {url}")


def request_json(
    method: str,
    url: str,
    *,
    body: dict[str, Any] | None = None,
    request_id: str | None = None,
    headers: dict[str, str] | None = None,
    expected_status: int = 200,
) -> dict[str, Any]:
    request_headers = dict(headers or {})
    if request_id is not None:
        request_headers["X-Request-Id"] = request_id
    data = None
    if body is not None:
        request_headers["Content-Type"] = "application/json"
        data = json.dumps(body).encode("utf-8")
    request = Request(url, data=data, headers=request_headers, method=method)
    try:
        with urlopen(request, timeout=10) as response:
            payload = json.loads(response.read().decode("utf-8"))
            if response.status != expected_status:
                raise AssertionError(f"{method} {url}: expected {expected_status}, got {response.status}")
            return payload
    except HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise AssertionError(f"{method} {url}: HTTP {error.code}: {detail}") from error


if __name__ == "__main__":
    main()
