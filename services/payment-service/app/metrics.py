class PaymentMetrics:
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
        self._payments_approved_total = 0
        self._payments_failed_total = 0

    def record_payment_approved(self) -> None:
        self._payments_approved_total += 1

    def record_payment_failed(self) -> None:
        self._payments_failed_total += 1

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
                    "payments_approved_total",
                    "Payments approved successfully.",
                    "counter",
                ),
                _metric_sample(
                    "payments_approved_total",
                    self._labels,
                    self._payments_approved_total,
                ),
                _metric_header("payments_failed_total", "Payments failed.", "counter"),
                _metric_sample("payments_failed_total", self._labels, self._payments_failed_total),
            ],
        )


def _metric_header(name: str, description: str, metric_type: str) -> str:
    return f"# HELP {name} {description}\n# TYPE {name} {metric_type}\n"


def _metric_sample(name: str, labels: str, value: int) -> str:
    return f"{name}{{{labels}}} {value}\n"
