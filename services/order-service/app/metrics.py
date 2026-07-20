from enum import StrEnum, unique
from typing import assert_never


@unique
class OutboxRelayOutcome(StrEnum):
    PUBLISHED = "published"
    RETRY = "retry"
    DEAD_LETTERED = "dead_lettered"


class OrderMetrics:
    def __init__(
        self,
        service_name: str,
        service_version: str,
        service_environment: str,
    ) -> None:
        self._labels = (
            f'service_name="{service_name}",'
            f'service_version="{service_version}",'
            f'service_environment="{service_environment}"'
        )
        self._orders_created_total = 0
        self._order_idempotency_replay_total = 0
        self._order_idempotency_conflict_total = 0
        self._orders_sold_out_total = 0
        self._outbox_published_total = 0
        self._outbox_retry_total = 0
        self._outbox_dead_lettered_total = 0
        self._expiry_missing_inventory_due = 0

    def record_order_created(self) -> None:
        self._orders_created_total += 1

    def record_idempotency_replay(self) -> None:
        self._order_idempotency_replay_total += 1

    def record_idempotency_conflict(self) -> None:
        self._order_idempotency_conflict_total += 1

    def record_sold_out(self) -> None:
        self._orders_sold_out_total += 1

    def record_outbox_relay(self, outcome: OutboxRelayOutcome) -> None:
        match outcome:
            case OutboxRelayOutcome.PUBLISHED:
                self._outbox_published_total += 1
            case OutboxRelayOutcome.RETRY:
                self._outbox_retry_total += 1
            case OutboxRelayOutcome.DEAD_LETTERED:
                self._outbox_dead_lettered_total += 1
            case unreachable:
                assert_never(unreachable)

    def set_expiry_missing_inventory_due(self, count: int) -> None:
        self._expiry_missing_inventory_due = count

    def render(self) -> str:
        return "".join(
            [
                _metric_header(
                    "orders_created_total",
                    "Orders created successfully.",
                    "counter",
                ),
                _metric_sample(
                    "orders_created_total", self._labels, self._orders_created_total
                ),
                _metric_header(
                    "order_idempotency_replay_total",
                    "Order create requests replayed from an idempotency key.",
                    "counter",
                ),
                _metric_sample(
                    "order_idempotency_replay_total",
                    self._labels,
                    self._order_idempotency_replay_total,
                ),
                _metric_header(
                    "order_idempotency_conflict_total",
                    (
                        "Order create requests rejected because an idempotency key "
                        "was reused with a different payload."
                    ),
                    "counter",
                ),
                _metric_sample(
                    "order_idempotency_conflict_total",
                    self._labels,
                    self._order_idempotency_conflict_total,
                ),
                _metric_header(
                    "orders_sold_out_total",
                    "Order create requests rejected because stock is sold out.",
                    "counter",
                ),
                _metric_sample(
                    "orders_sold_out_total", self._labels, self._orders_sold_out_total
                ),
                _metric_header(
                    "order_outbox_relay_total",
                    "Order outbox relay attempts by bounded outcome.",
                    "counter",
                ),
                _metric_sample(
                    "order_outbox_relay_total",
                    f'{self._labels},outcome="published"',
                    self._outbox_published_total,
                ),
                _metric_sample(
                    "order_outbox_relay_total",
                    f'{self._labels},outcome="retry"',
                    self._outbox_retry_total,
                ),
                _metric_sample(
                    "order_outbox_relay_total",
                    f'{self._labels},outcome="dead_lettered"',
                    self._outbox_dead_lettered_total,
                ),
                _metric_header(
                    "order_expiry_missing_inventory_due",
                    "Whether a due pending order lacks its inventory row.",
                    "gauge",
                ),
                _metric_sample(
                    "order_expiry_missing_inventory_due",
                    self._labels,
                    self._expiry_missing_inventory_due,
                ),
            ],
        )


def _metric_header(name: str, description: str, metric_type: str) -> str:
    return f"# HELP {name} {description}\n# TYPE {name} {metric_type}\n"


def _metric_sample(name: str, labels: str, value: int) -> str:
    return f"{name}{{{labels}}} {value}\n"
