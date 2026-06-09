from blinker import ANY
from prometheus_client import CollectorRegistry

from app.metrics.prometheus import (
    payment_events_published_total,
    payment_request_duration_seconds,
    payments_total,
)
from app.metrics.telemetry_events import (
    PaymentEventPublishRecorded,
    PaymentRecorded,
    payment_event_publish_recorded,
    payment_recorded,
)


class PaymentMetricsAdapter:
    def __init__(self, *, registry: CollectorRegistry, service_name: str, service_environment: str) -> None:
        """결제 telemetry event를 기록할 Prometheus metric 핸들을 준비한다."""
        _require_service_label("service_name", service_name)
        _require_service_label("service_environment", service_environment)
        self._service_labels = {
            "service_name": service_name,
            "service_environment": service_environment,
        }
        self._payments_total = payments_total(registry)
        self._payment_duration = payment_request_duration_seconds(registry)
        self._payment_events_published_total = payment_events_published_total(registry)

    def connect(self) -> None:
        """결제 telemetry signal과 Prometheus 기록 함수를 연결한다."""
        payment_recorded.connect(self.record_payment, sender=ANY, weak=False)
        payment_event_publish_recorded.connect(self.record_payment_event_publish, sender=ANY, weak=False)

    def record_payment(self, sender: object, *, event: PaymentRecorded) -> None:
        """결제 결과 counter와 처리 시간 histogram을 기록한다."""
        labels = {
            **self._service_labels,
            "method": event.method.value,
            "result": event.result.value,
            "error_code": event.error_code.value,
            "failure_kind": event.failure_kind.value,
            "retryable": event.retryable.value,
        }
        self._payments_total.labels(**labels).inc()
        self._payment_duration.labels(
            **self._service_labels,
            method=event.method.value,
            result=event.result.value,
        ).observe(event.duration_seconds)

    def record_payment_event_publish(self, sender: object, *, event: PaymentEventPublishRecorded) -> None:
        """결제 이벤트 발행 결과 counter를 기록한다."""
        self._payment_events_published_total.labels(
            **self._service_labels,
            event_type=event.event_type.value,
            result=event.result.value,
        ).inc()


def configure_payment_metrics(
    registry: CollectorRegistry,
    *,
    service_name: str,
    service_environment: str,
) -> PaymentMetricsAdapter:
    """payment-service 전용 Prometheus metric adapter를 등록한다."""
    adapter = PaymentMetricsAdapter(
        registry=registry,
        service_name=service_name,
        service_environment=service_environment,
    )
    adapter.connect()
    return adapter


def _require_service_label(name: str, value: str | None) -> None:
    """metric에 필요한 서비스 label 값이 비어 있지 않은지 확인한다."""
    if value is None or value == "":
        raise ValueError(f"{name} is required")
