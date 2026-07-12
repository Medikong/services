from metrics import (
    CommonServiceLabel,
    MetricHandleMap,
    MetricLabelEvent,
    connect_metrics_for_events,
    create_metrics_for_events,
)
from prometheus_client import CollectorRegistry

from app.metrics.events import (
    CatalogQueryRecorded,
    ConcertAdminCommandRecorded,
    SeatInventoryCommandRecorded,
)
from app.metrics.recorder import concert_telemetry_recorded


CONCERT_SERVICE_LABELS = (
    CommonServiceLabel.SERVICE_NAME.value,
    CommonServiceLabel.SERVICE_ENVIRONMENT.value,
)
CONCERT_METRIC_EVENTS: tuple[type[MetricLabelEvent], ...] = (
    CatalogQueryRecorded,
    ConcertAdminCommandRecorded,
    SeatInventoryCommandRecorded,
)


class ConcertMetricsAdapter:
    def __init__(self, *, registry: CollectorRegistry, service_name: str, service_environment: str) -> None:
        """공연 telemetry event를 기록할 Prometheus metric 핸들을 준비한다."""
        _require_service_label("service_name", service_name)
        _require_service_label("service_environment", service_environment)
        self._service_labels = {"service_name": service_name, "service_environment": service_environment}
        self._metrics: MetricHandleMap = create_metrics_for_events(
            registry,
            service_label_names=CONCERT_SERVICE_LABELS,
            event_types=CONCERT_METRIC_EVENTS,
        )

    def connect(self) -> None:
        """공연 telemetry signal과 Prometheus 기록 함수를 연결한다."""
        connect_metrics_for_events(
            concert_telemetry_recorded,
            self._metrics,
            self._service_labels,
            CONCERT_METRIC_EVENTS,
        )


def configure_concert_metrics(
    registry: CollectorRegistry,
    *,
    service_name: str,
    service_environment: str,
) -> ConcertMetricsAdapter:
    """concert-service 전용 Prometheus metric adapter를 등록한다."""
    adapter = ConcertMetricsAdapter(registry=registry, service_name=service_name, service_environment=service_environment)
    adapter.connect()
    return adapter


def _require_service_label(name: str, value: str | None) -> None:
    """metric에 필요한 서비스 label 값이 비어 있지 않은지 확인한다."""
    if value is None or value == "":
        raise ValueError(f"{name} is required")
