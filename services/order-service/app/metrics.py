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

    def record_order_created(self) -> None:
        self._orders_created_total += 1

    def record_idempotency_replay(self) -> None:
        self._order_idempotency_replay_total += 1

    def record_idempotency_conflict(self) -> None:
        self._order_idempotency_conflict_total += 1

    def record_sold_out(self) -> None:
        self._orders_sold_out_total += 1

    def render(self) -> str:
        return "".join(
            [
                _metric_header(
                    "service_ready",
                    "Service readiness state. Ready is 1, not ready is 0.",
                    "gauge",
                ),
                _metric_sample("service_ready", self._labels, 1),
                _metric_header(
                    "orders_created_total",
                    "Orders created successfully.",
                    "counter",
                ),
                _metric_sample("orders_created_total", self._labels, self._orders_created_total),
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
                _metric_sample("orders_sold_out_total", self._labels, self._orders_sold_out_total),
            ],
        )


def _metric_header(name: str, description: str, metric_type: str) -> str:
    return f"# HELP {name} {description}\n# TYPE {name} {metric_type}\n"


def _metric_sample(name: str, labels: str, value: int) -> str:
    return f"{name}{{{labels}}} {value}\n"
