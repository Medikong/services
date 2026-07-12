from __future__ import annotations

import json
import os
import sys
from dataclasses import dataclass
from threading import Barrier, BrokenBarrierError, Lock, Thread
from typing import Final, NotRequired, TypedDict
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

DROP_ID: Final = "drop-sold-out-001"
PRODUCT_ID: Final = "product-sold-out-001"
QUANTITY: Final = 10
REQUEST_COUNT: Final = 5
EXPECTED_DISTRIBUTION_ITEMS: Final = ((201, 4), (409, 1))


class SmokeOutput(TypedDict):
    drop_id: str
    error: NotRequired[str]
    expected_status_distribution: dict[int, int]
    ok: bool
    product_id: str
    quantity: int
    request_count: int
    run_id: str
    status_distribution: dict[int, int]
    statuses: list[int]


@dataclass(frozen=True, slots=True)
class OrderAttempt:
    index: int
    user_id: str
    idempotency_key: str


@dataclass(frozen=True, slots=True)
class OrderResult:
    index: int
    status: int
    body: str


@dataclass(frozen=True, slots=True)
class ConcurrentOrderError(Exception):
    errors: tuple[str, ...]

    def __str__(self) -> str:
        return ";".join(self.errors)


def build_attempts(run_id: str) -> tuple[OrderAttempt, ...]:
    """Build the five distinct users and idempotency keys for one smoke run."""
    return tuple(
        OrderAttempt(
            index=index,
            user_id=f"{run_id}-user-{index}",
            idempotency_key=f"{run_id}-order-{index}",
        )
        for index in range(1, REQUEST_COUNT + 1)
    )


def create_order(order_service_url: str, attempt: OrderAttempt) -> OrderResult:
    """Submit one order create request and return its HTTP status."""
    payload = json.dumps(
        {
            "dropId": DROP_ID,
            "productId": PRODUCT_ID,
            "quantity": QUANTITY,
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    request = Request(
        f"{order_service_url.rstrip('/')}/orders",
        data=payload,
        headers={
            "Content-Type": "application/json",
            "X-Request-Id": f"{attempt.idempotency_key}-request",
            "X-User-Id": attempt.user_id,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": attempt.idempotency_key,
        },
        method="POST",
    )
    try:
        with urlopen(request, timeout=30) as response:
            return OrderResult(
                index=attempt.index,
                status=response.status,
                body=response.read().decode("utf-8"),
            )
    except HTTPError as exc:
        return OrderResult(
            index=attempt.index,
            status=exc.code,
            body=exc.read().decode("utf-8"),
        )


def run_concurrent_orders(
    order_service_url: str,
    attempts: tuple[OrderAttempt, ...],
) -> tuple[OrderResult, ...]:
    """Release all order requests through a barrier and collect their statuses."""
    barrier = Barrier(len(attempts) + 1)
    lock = Lock()
    results: list[OrderResult] = []
    errors: list[str] = []

    def worker(attempt: OrderAttempt) -> None:
        try:
            barrier.wait()
            result = create_order(order_service_url, attempt)
        except (BrokenBarrierError, URLError, TimeoutError) as exc:
            with lock:
                errors.append(f"{attempt.index}:{type(exc).__name__}:{exc}")
            return
        with lock:
            results.append(result)

    threads = [Thread(target=worker, args=(attempt,)) for attempt in attempts]
    for thread in threads:
        thread.start()
    try:
        try:
            barrier.wait()
        except BrokenBarrierError as exc:
            errors.append(f"main:{type(exc).__name__}:{exc}")
    finally:
        for thread in threads:
            thread.join()
    if errors:
        raise ConcurrentOrderError(errors=tuple(sorted(errors)))
    return tuple(sorted(results, key=lambda result: result.index))


def status_distribution(results: tuple[OrderResult, ...]) -> dict[int, int]:
    """Count response statuses from the concurrent order attempts."""
    distribution: dict[int, int] = {}
    for result in results:
        distribution[result.status] = distribution.get(result.status, 0) + 1
    return dict(sorted(distribution.items()))


def main() -> int:
    """Run the concurrent purchase smoke and emit deterministic JSON."""
    order_service_url = (
        sys.argv[1]
        if len(sys.argv) > 1
        else os.environ.get("ORDER_SERVICE_URL", "http://order-service:8082")
    )
    run_id = os.environ.get("PURCHASE_CONCURRENCY_RUN_ID", "purchase-concurrency")
    attempts = build_attempts(run_id)
    expected_distribution = dict(EXPECTED_DISTRIBUTION_ITEMS)
    try:
        results = run_concurrent_orders(order_service_url, attempts)
        distribution = status_distribution(results)
        ok = distribution == expected_distribution
        output: SmokeOutput = {
            "drop_id": DROP_ID,
            "expected_status_distribution": expected_distribution,
            "ok": ok,
            "product_id": PRODUCT_ID,
            "quantity": QUANTITY,
            "request_count": REQUEST_COUNT,
            "run_id": run_id,
            "status_distribution": distribution,
            "statuses": [result.status for result in results],
        }
    except ConcurrentOrderError as exc:
        ok = False
        output = {
            "drop_id": DROP_ID,
            "error": str(exc),
            "expected_status_distribution": expected_distribution,
            "ok": ok,
            "product_id": PRODUCT_ID,
            "quantity": QUANTITY,
            "request_count": REQUEST_COUNT,
            "run_id": run_id,
            "status_distribution": {},
            "statuses": [],
        }
    print(json.dumps(output, sort_keys=True, separators=(",", ":")))
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
