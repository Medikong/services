from __future__ import annotations

from dataclasses import dataclass

from payment_failure_idempotency_runner_support import RunnerConfig, SmokeResult


@dataclass(frozen=True, slots=True)
class _DatabaseCheck:
    label: str
    database: str
    query: str
    expected: str
    failure_label: str


def _database_checks(
    config: RunnerConfig, smoke: SmokeResult
) -> tuple[_DatabaseCheck, ...]:
    return (
        _DatabaseCheck(
            "payment_rows|distinct_ids|min_id|max_id",
            "payment_service",
            "SELECT COUNT(*), COUNT(DISTINCT id), COALESCE(MIN(id), ''), "
            "COALESCE(MAX(id), '') FROM payments "
            f"WHERE user_id = '{smoke.user_id}' AND "
            f"idempotency_key = '{config.run_id}-payment-failure';",
            f"1|1|{smoke.payment_id}|{smoke.payment_id}",
            "Payment SQL assertion",
        ),
        _DatabaseCheck(
            "processed_events|distinct_event_ids|min_event_type|min_aggregate_type|min_aggregate_id",
            "order_service",
            "SELECT COUNT(*), COUNT(DISTINCT event_id), "
            "COALESCE(MIN(event_type), ''), COALESCE(MIN(aggregate_type), ''), "
            "COALESCE(MIN(aggregate_id), '') "
            "FROM processed_events "
            f"WHERE event_id = '{smoke.event_id}';",
            f"1|1|payment.failed|order|{smoke.order_id}",
            "Processed event SQL assertion",
        ),
        _DatabaseCheck(
            "order_rows|status|payment_id",
            "order_service",
            "SELECT COUNT(*), COALESCE(MIN(status), ''), "
            "COALESCE(MIN(payment_id), '') FROM orders "
            f"WHERE id = '{smoke.order_id}' AND user_id = '{smoke.user_id}';",
            f"1|PAYMENT_FAILED|{smoke.payment_id}",
            "Order SQL assertion",
        ),
    )
