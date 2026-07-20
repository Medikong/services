class NotificationMetrics:
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
        self._events_consumed_total = 0
        self._notifications_created_total = 0
        self._events_replayed_total = 0
        self._events_invalid_total = 0

    def record_created(self) -> None:
        self._events_consumed_total += 1
        self._notifications_created_total += 1

    def record_replayed(self) -> None:
        self._events_consumed_total += 1
        self._events_replayed_total += 1

    def record_invalid(self) -> None:
        self._events_consumed_total += 1
        self._events_invalid_total += 1

    def render(self) -> str:
        return "".join(
            [
                _metric_header(
                    "notification_requested_events_consumed_total",
                    "Notification requested Kafka events consumed.",
                    "counter",
                ),
                _metric_sample(
                    "notification_requested_events_consumed_total",
                    self._labels,
                    self._events_consumed_total,
                ),
                _metric_header(
                    "notifications_created_total",
                    "Notifications created from requested events.",
                    "counter",
                ),
                _metric_sample(
                    "notifications_created_total",
                    self._labels,
                    self._notifications_created_total,
                ),
                _metric_header(
                    "notification_requested_events_replayed_total",
                    "Notification requested events replayed without duplicate creation.",
                    "counter",
                ),
                _metric_sample(
                    "notification_requested_events_replayed_total",
                    self._labels,
                    self._events_replayed_total,
                ),
                _metric_header(
                    "notification_requested_events_invalid_total",
                    "Invalid notification requested events rejected.",
                    "counter",
                ),
                _metric_sample(
                    "notification_requested_events_invalid_total",
                    self._labels,
                    self._events_invalid_total,
                ),
            ],
        )


def _metric_header(name: str, description: str, metric_type: str) -> str:
    return f"# HELP {name} {description}\n# TYPE {name} {metric_type}\n"


def _metric_sample(name: str, labels: str, value: int) -> str:
    return f"{name}{{{labels}}} {value}\n"
