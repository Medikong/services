from dataclasses import dataclass

from blinker import Namespace
from metrics import FailureKind, MetricResult, Retryable

from app.metrics.labels import PaymentErrorCode, PaymentEventType, PaymentMethod


payment_signals = Namespace()
payment_recorded = payment_signals.signal("payment.recorded")
payment_event_publish_recorded = payment_signals.signal("payment.event_publish_recorded")


@dataclass(frozen=True)
class PaymentRecorded:
    method: PaymentMethod
    result: MetricResult
    error_code: PaymentErrorCode
    failure_kind: FailureKind
    retryable: Retryable
    duration_seconds: float


@dataclass(frozen=True)
class PaymentEventPublishRecorded:
    event_type: PaymentEventType
    result: MetricResult


def publish_payment_recorded(event: PaymentRecorded) -> None:
    """결제 처리 결과 telemetry event를 프로세스 내부 signal로 발행한다."""
    payment_recorded.send("payment-service", event=event)


def publish_payment_event_publish_recorded(event: PaymentEventPublishRecorded) -> None:
    """결제 이벤트 발행 결과 telemetry event를 프로세스 내부 signal로 발행한다."""
    payment_event_publish_recorded.send("payment-service", event=event)
