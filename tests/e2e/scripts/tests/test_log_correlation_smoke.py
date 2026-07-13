from __future__ import annotations

import sys
from pathlib import Path

import pytest


SMOKE_DIR = Path(__file__).resolve().parents[2] / "log-correlation"
sys.path.insert(0, str(SMOKE_DIR))

import smoke  # noqa: E402


@pytest.mark.parametrize(
    ("fail", "payment_path", "terminal_status"),
    [
        (False, "mock-approvals", "CONFIRMED"),
        (True, "mock-failures", "PAYMENT_FAILED"),
    ],
)
def test_run_purchase_waits_until_ready_before_payment(
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
        events.append(f"wait:{expected}")

    monkeypatch.setattr(smoke, "request_json", fake_request_json)
    monkeypatch.setattr(smoke, "wait_order_status", fake_wait_order_status)

    smoke.run_purchase("drop-1", "product-1", 1000, fail=fail)

    assert events == [
        "order-created",
        "wait:READY_FOR_PAYMENT",
        f"payment-posted:{payment_path}",
        f"wait:{terminal_status}",
    ]
