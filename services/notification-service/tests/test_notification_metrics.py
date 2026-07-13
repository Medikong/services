from app.metrics import NotificationMetrics


def test_notification_metrics_start_at_zero() -> None:
    # Given
    metrics = NotificationMetrics("notification-service", "test", "test")

    # When
    rendered = metrics.render()

    # Then
    assert _metric_value(rendered, "notification_requested_events_consumed_total") == 0
    assert _metric_value(rendered, "notifications_created_total") == 0
    assert _metric_value(rendered, "notification_requested_events_replayed_total") == 0
    assert _metric_value(rendered, "notification_requested_events_invalid_total") == 0


def test_notification_metrics_record_consumer_outcomes() -> None:
    # Given
    metrics = NotificationMetrics("notification-service", "test", "test")

    # When
    metrics.record_created()
    metrics.record_replayed()
    metrics.record_invalid()

    # Then
    rendered = metrics.render()
    assert _metric_value(rendered, "notification_requested_events_consumed_total") == 3
    assert _metric_value(rendered, "notifications_created_total") == 1
    assert _metric_value(rendered, "notification_requested_events_replayed_total") == 1
    assert _metric_value(rendered, "notification_requested_events_invalid_total") == 1


def _metric_value(rendered: str, name: str) -> int:
    sample = next(line for line in rendered.splitlines() if line.startswith(f"{name}{'{'}"))
    return int(sample.rsplit(" ", maxsplit=1)[1])
