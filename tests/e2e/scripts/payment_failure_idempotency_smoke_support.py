from __future__ import annotations

import json

from payment_failure_idempotency_runner_support import (
    RunnerExecutionError,
    SmokeResult,
    validate_name,
)


def parse_smoke_json(payload: str, expected_run_id: str) -> SmokeResult:
    """Parse smoke JSON and constrain every value later used by SQL."""
    try:
        decoded = json.loads(payload)
    except json.JSONDecodeError as exc:
        raise RunnerExecutionError(f"smoke output is not JSON: {exc}") from exc
    match decoded:  # noqa: MATCH_OK - open JSON boundary rejects unknown shapes.
        case {
            "ok": True,
            "order_id": str(order_id),
            "payment_id": str(payment_id),
            "run_id": str(run_id),
            "unique_event_ids": [str(event_id)],
            "user_id": str(user_id),
        }:
            if run_id != expected_run_id:
                raise RunnerExecutionError(
                    f"smoke run_id mismatch: expected {expected_run_id}, got {run_id}",
                )
            return SmokeResult(
                order_id=validate_name("order_id", order_id),
                payment_id=validate_name("payment_id", payment_id),
                user_id=validate_name("user_id", user_id),
                event_id=validate_name("event_id", event_id),
            )
        case _:
            raise RunnerExecutionError("smoke JSON is unsuccessful or has an invalid shape")
