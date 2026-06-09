from time import perf_counter

from metrics import FailureKind, MetricResult, Retryable

from app.metrics.labels import PaymentErrorCode, PaymentEventType, payment_method_label
from app.metrics.telemetry_events import (
    PaymentEventPublishRecorded,
    PaymentRecorded,
    publish_payment_event_publish_recorded,
    publish_payment_recorded,
)


class PaymentTelemetryRecorder:
    def start_payment(self, method: str) -> "PaymentAttemptRecorder":
        """결제 시도 metric 기록기를 시작한다."""
        # 결제 요청 단위로 처리 시간과 최종 결과를 추적한다.
        return PaymentAttemptRecorder(method)

    def record_event_publish_success(self, event_type: PaymentEventType) -> None:
        """결제 이벤트 발행 성공을 telemetry event로 남긴다."""
        # Kafka 발행 성공은 결제 결과와 별도 metric으로 남긴다.
        publish_payment_event_publish_recorded(
            PaymentEventPublishRecorded(event_type=event_type, result=MetricResult.SUCCESS)
        )

    def record_event_publish_failure(self, event_type: PaymentEventType) -> None:
        """결제 이벤트 발행 실패를 telemetry event로 남긴다."""
        # 발행 실패도 예외 흐름은 유지하고 metric만 추가로 남긴다.
        publish_payment_event_publish_recorded(
            PaymentEventPublishRecorded(event_type=event_type, result=MetricResult.FAILURE)
        )


class PaymentAttemptRecorder:
    def __init__(self, method: str) -> None:
        """결제 시도 metric의 기본 실패 상태를 준비한다."""
        # method는 낮은 cardinality label 값으로 정규화한다.
        self._started_at = perf_counter()
        self._method = payment_method_label(method)
        self._result = MetricResult.FAILURE
        self._error_code = PaymentErrorCode.INTERNAL_ERROR
        self._failure_kind = FailureKind.INTERNAL_ERROR
        self._retryable = Retryable.FALSE

    def mark_duplicate(self) -> None:
        """idempotency 재사용 결과로 metric 상태를 바꾼다."""
        # idempotency 재사용은 실패가 아니라 duplicate 결과로 분리한다.
        self._result = MetricResult.DUPLICATE
        self._error_code = PaymentErrorCode.NONE
        self._failure_kind = FailureKind.NONE
        self._retryable = Retryable.FALSE

    def mark_invalid_simulation(self) -> None:
        """잘못된 시뮬레이션 입력으로 metric 상태를 바꾼다."""
        # 잘못된 입력은 운영 장애가 아닌 비즈니스 거절로 본다.
        self._result = MetricResult.REJECTION
        self._error_code = PaymentErrorCode.INVALID_SIMULATION
        self._failure_kind = FailureKind.BUSINESS_REJECTION
        self._retryable = Retryable.FALSE

    def mark_payment_status(self, payment_status: str) -> None:
        """결제 상태에 맞춰 metric label 값을 확정한다."""
        # 결제 상태별 성공/실패/지연 label 조합을 확정한다.
        (
            self._result,
            self._error_code,
            self._failure_kind,
            self._retryable,
        ) = _payment_metric_outcome(payment_status)

    def record(self) -> None:
        """결제 시도 metric을 최종 기록한다."""
        # 최종 metric 기록은 한 곳에서만 수행한다.
        publish_payment_recorded(
            PaymentRecorded(
                method=self._method,
                result=self._result,
                error_code=self._error_code,
                failure_kind=self._failure_kind,
                retryable=self._retryable,
                duration_seconds=perf_counter() - self._started_at,
            )
        )


def _payment_metric_outcome(payment_status: str) -> tuple[MetricResult, PaymentErrorCode, FailureKind, Retryable]:
    """결제 상태를 Prometheus label enum 조합으로 변환한다."""
    # payment_status를 Prometheus label로 사용할 고정 enum 값으로 변환한다.
    if payment_status == "approved":
        return MetricResult.SUCCESS, PaymentErrorCode.NONE, FailureKind.NONE, Retryable.FALSE
    if payment_status == "failed":
        return MetricResult.FAILURE, PaymentErrorCode.FAILED, FailureKind.BUSINESS_REJECTION, Retryable.FALSE
    if payment_status == "delayed":
        return MetricResult.DELAYED, PaymentErrorCode.DELAYED, FailureKind.DEPENDENCY_ERROR, Retryable.TRUE
    return MetricResult.FAILURE, PaymentErrorCode.INTERNAL_ERROR, FailureKind.INTERNAL_ERROR, Retryable.FALSE
