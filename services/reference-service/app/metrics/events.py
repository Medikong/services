from dataclasses import dataclass

from metrics import CounterMetricSpec, HistogramMetricSpec, MetricResult, MetricSpec

from app.metrics.labels import CatalogResource, ConcertAdminCommand, SeatInventoryCommand


@dataclass(frozen=True)
class CatalogQueryRecorded:
    resource: CatalogResource
    result: MetricResult
    duration_seconds: float

    @classmethod
    def metric_specs(cls) -> tuple[MetricSpec, ...]:
        """공개 조회 event가 기록할 metric spec을 반환한다."""
        return (
            CounterMetricSpec(
                name="catalog_queries_total",
                description="Catalog queries by resource and result.",
                label_fields={
                    "resource": "resource",
                    "result": "result",
                },
            ),
            HistogramMetricSpec(
                name="catalog_query_duration_seconds",
                description="Catalog query duration in seconds.",
                label_fields={
                    "resource": "resource",
                    "result": "result",
                },
                value_field="duration_seconds",
            ),
        )


@dataclass(frozen=True)
class ConcertAdminCommandRecorded:
    command: ConcertAdminCommand
    result: MetricResult

    @classmethod
    def metric_specs(cls) -> tuple[MetricSpec, ...]:
        """관리자/공급자 command event가 기록할 metric spec을 반환한다."""
        return (
            CounterMetricSpec(
                name="concert_admin_commands_total",
                description="Concert admin commands by command and result.",
                label_fields={
                    "command": "command",
                    "result": "result",
                },
            ),
        )


@dataclass(frozen=True)
class SeatInventoryCommandRecorded:
    command: SeatInventoryCommand
    result: MetricResult

    @classmethod
    def metric_specs(cls) -> tuple[MetricSpec, ...]:
        """좌석 재고 command event가 기록할 metric spec을 반환한다."""
        return (
            CounterMetricSpec(
                name="seat_inventory_commands_total",
                description="Seat inventory commands by command and result.",
                label_fields={
                    "command": "command",
                    "result": "result",
                },
            ),
        )
