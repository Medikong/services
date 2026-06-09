from enum import StrEnum

from prometheus_client import CollectorRegistry, Counter, Histogram

from metrics.labels import CommonServiceLabel, assert_safe_metric_label_names


class PaymentLabel(StrEnum):
    SERVICE_NAME = CommonServiceLabel.SERVICE_NAME.value
    SERVICE_ENVIRONMENT = CommonServiceLabel.SERVICE_ENVIRONMENT.value
    METHOD = "method"
    RESULT = "result"
    ERROR_CODE = "error_code"
    FAILURE_KIND = "failure_kind"
    RETRYABLE = "retryable"


class PaymentRequestDurationLabel(StrEnum):
    SERVICE_NAME = CommonServiceLabel.SERVICE_NAME.value
    SERVICE_ENVIRONMENT = CommonServiceLabel.SERVICE_ENVIRONMENT.value
    METHOD = "method"
    RESULT = "result"


class PaymentEventPublishedLabel(StrEnum):
    SERVICE_NAME = CommonServiceLabel.SERVICE_NAME.value
    SERVICE_ENVIRONMENT = CommonServiceLabel.SERVICE_ENVIRONMENT.value
    EVENT_TYPE = "event_type"
    RESULT = "result"


PAYMENT_LABELS = tuple(label.value for label in PaymentLabel)
PAYMENT_REQUEST_DURATION_LABELS = tuple(label.value for label in PaymentRequestDurationLabel)
PAYMENT_EVENT_PUBLISHED_LABELS = tuple(label.value for label in PaymentEventPublishedLabel)
assert_safe_metric_label_names(PAYMENT_LABELS)
assert_safe_metric_label_names(PAYMENT_REQUEST_DURATION_LABELS)
assert_safe_metric_label_names(PAYMENT_EVENT_PUBLISHED_LABELS)


def payments_total(registry: CollectorRegistry) -> Counter:
    """결제 시도 결과를 집계하는 counter를 생성한다."""
    return Counter(
        "payments_total",
        "Payment attempts by result.",
        PAYMENT_LABELS,
        registry=registry,
    )


def payment_request_duration_seconds(registry: CollectorRegistry) -> Histogram:
    """결제 API 처리 시간을 관측하는 histogram을 생성한다."""
    return Histogram(
        "payment_request_duration_seconds",
        "Payment request duration in seconds.",
        PAYMENT_REQUEST_DURATION_LABELS,
        registry=registry,
    )


def payment_events_published_total(registry: CollectorRegistry) -> Counter:
    """결제 이벤트 발행 결과를 집계하는 counter를 생성한다."""
    return Counter(
        "payment_events_published_total",
        "Payment event publish attempts by result.",
        PAYMENT_EVENT_PUBLISHED_LABELS,
        registry=registry,
    )
