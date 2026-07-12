from time import perf_counter

from blinker import Namespace
from metrics import MetricLabelEvent, MetricResult

from app.metrics.events import CatalogQueryRecorded
from app.metrics.labels import CatalogResource


concert_signals = Namespace()
concert_telemetry_recorded = concert_signals.signal("concert.telemetry_recorded")


class ConcertTelemetryRecorder:
    def __init__(self, sender: str = "concert-service") -> None:
        """공연 telemetry signal sender를 준비한다."""
        self._sender = sender

    def start_catalog_query(self, resource: CatalogResource) -> "CatalogQueryAttemptRecorder":
        """공개 조회 metric 기록기를 시작한다."""
        return CatalogQueryAttemptRecorder(resource, recorder=self)

    def record(self, event: MetricLabelEvent) -> None:
        """공연 telemetry event를 단일 signal로 발행한다."""
        concert_telemetry_recorded.send(self._sender, event=event)


class CatalogQueryAttemptRecorder:
    def __init__(self, resource: CatalogResource, *, recorder: ConcertTelemetryRecorder) -> None:
        """공개 조회 metric의 기본 실패 상태를 준비한다."""
        self._recorder = recorder
        self._started_at = perf_counter()
        self._resource = resource
        self._result = MetricResult.FAILURE

    def mark_success(self) -> None:
        """공개 조회 성공 상태로 metric label을 확정한다."""
        self._result = MetricResult.SUCCESS

    def mark_rejection(self) -> None:
        """공개 조회 거절 상태로 metric label을 확정한다."""
        self._result = MetricResult.REJECTION

    def record(self) -> None:
        """공개 조회 metric을 최종 기록한다."""
        self._recorder.record(
            CatalogQueryRecorded(
                resource=self._resource,
                result=self._result,
                duration_seconds=perf_counter() - self._started_at,
            )
        )
