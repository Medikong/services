import pytest
from dataclasses import dataclass
from enum import StrEnum
from prometheus_client import CollectorRegistry, Counter, Histogram, generate_latest

from metrics import (
    CounterMetricSpec,
    HistogramMetricSpec,
    HTTP_ACTIVE_REQUEST_LABELS,
    HTTP_REQUEST_DURATION_LABELS,
    MetricSpec,
    SERVICE_READY_LABELS,
    ServiceIdentity,
    assert_safe_metric_label_names,
    bounded_http_method,
    connect_counter,
    connect_histogram,
    connect_metric,
    connect_metrics_for_events,
    create_metrics_for_events,
    metric_label_names_for_spec,
    metric_label_names_from_fields,
    metric_labels_for_event,
    metric_labels_from_fields,
    metric_spec_for,
    http_server_active_requests,
    http_server_request_duration_seconds,
    service_ready,
)


def test_common_http_label_names_match_prometheus_policy() -> None:
    assert HTTP_REQUEST_DURATION_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
        "http_route",
        "http_route_kind",
        "http_request_method",
        "http_response_status_code",
    )


@pytest.mark.parametrize(
    ("method", "expected"),
    (("get", "GET"), ("PATCH", "PATCH"), ("X-CARDINALITY-1", "OTHER")),
)
def test_http_method_metric_label_is_bounded(method: str, expected: str) -> None:
    assert bounded_http_method(method) == expected
    assert HTTP_ACTIVE_REQUEST_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
        "http_route",
        "http_route_kind",
        "http_request_method",
    )
    assert SERVICE_READY_LABELS == (
        "service_name",
        "service_version",
        "service_environment",
    )


def test_common_operational_metric_factories_create_prometheus_handles() -> None:
    registry = CollectorRegistry(auto_describe=True)

    http_server_request_duration_seconds(registry)
    http_server_active_requests(registry)
    ready_metric = service_ready(registry)
    ready_metric.labels(
        service_name="payment-service",
        service_version="test",
        service_environment="local",
    ).set(1)

    metrics_text = generate_latest(registry).decode("utf-8")

    assert "# TYPE http_server_request_duration_seconds histogram" in metrics_text
    assert "# TYPE http_server_active_requests gauge" in metrics_text
    assert "# TYPE service_ready gauge" in metrics_text
    assert 'service_name="payment-service"' in metrics_text


def test_service_identity_requires_service_version_and_environment() -> None:
    identity = ServiceIdentity(
        service_name="payment-service",
        service_version="test-version",
        service_environment="test",
    )

    assert identity.service_labels() == {
        "service_name": "payment-service",
        "service_version": "test-version",
        "service_environment": "test",
    }

    with pytest.raises(ValueError, match="service_version"):
        ServiceIdentity(
            service_name="payment-service",
            service_version="",
            service_environment="test",
        )

    with pytest.raises(ValueError, match="service_environment"):
        ServiceIdentity(
            service_name="payment-service",
            service_version="test-version",
            service_environment="",
        )


@pytest.mark.parametrize(
    "label_name",
    (
        "request_id",
        "reservation_id",
        "ticket_id",
        "drop_id",
        "coupon_id",
    ),
)
def test_high_cardinality_labels_are_rejected(label_name: str) -> None:
    with pytest.raises(ValueError, match=label_name):
        assert_safe_metric_label_names(["service_name", label_name])


def test_metric_label_event_helpers_use_class_schema() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result
        route_kind: str

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="sample_events_total",
                    description="Sample events.",
                    label_fields={
                        "result": "result",
                        "route_kind": "route_kind",
                    },
                ),
            )

    event = SampleEvent(result=Result.SUCCESS, route_kind="list")
    spec = metric_spec_for(SampleEvent, CounterMetricSpec)

    assert metric_label_names_for_spec(spec) == ("result", "route_kind")
    assert metric_label_names_from_fields(spec.label_fields) == ("result", "route_kind")
    assert metric_labels_from_fields(event, spec.label_fields) == {
        "result": "success",
        "route_kind": "list",
    }


def test_metric_signal_connectors_use_event_label_schema() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    class SampleSignal:
        def __init__(self) -> None:
            self.receivers = []

        def connect(self, receiver, *, weak: bool) -> None:
            self.receivers.append(receiver)

        def send(self, event: object) -> None:
            for receiver in self.receivers:
                receiver("test-service", event=event)

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result
        route_kind: str
        duration_seconds: float

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="sample_events_total",
                    description="Sample events.",
                    label_fields={"result": "result", "route_kind": "route_kind"},
                ),
                HistogramMetricSpec(
                    name="sample_duration_seconds",
                    description="Sample duration.",
                    label_fields={"result": "result"},
                    value_field="duration_seconds",
                ),
            )

    @dataclass(frozen=True)
    class OtherEvent:
        result: Result

    registry = CollectorRegistry(auto_describe=True)
    counter_signal = SampleSignal()
    histogram_signal = SampleSignal()
    service_labels = {"service_name": "sample-service"}
    counter = Counter(
        "sample_events_total",
        "Sample events.",
        ("service_name", "result", "route_kind"),
        registry=registry,
    )
    histogram = Histogram(
        "sample_duration_seconds",
        "Sample duration.",
        ("service_name", "result"),
        registry=registry,
    )

    counter_spec = metric_spec_for(SampleEvent, CounterMetricSpec)
    histogram_spec = metric_spec_for(SampleEvent, HistogramMetricSpec)

    connect_counter(
        counter_signal, counter, service_labels, SampleEvent, spec=counter_spec
    )
    connect_histogram(
        histogram_signal,
        histogram,
        service_labels,
        SampleEvent,
        value_field="duration_seconds",
        spec=histogram_spec,
    )

    event = SampleEvent(result=Result.SUCCESS, route_kind="list", duration_seconds=0.5)
    counter_signal.send(OtherEvent(result=Result.SUCCESS))
    counter_signal.send(event)
    histogram_signal.send(event)
    metrics_text = generate_latest(registry).decode("utf-8")

    assert "sample_events_total" in metrics_text
    assert "sample_duration_seconds_count" in metrics_text
    assert 'service_name="sample-service"' in metrics_text
    assert 'result="success"' in metrics_text
    assert 'route_kind="list"' in metrics_text


def test_metric_signal_connectors_accept_custom_record_callbacks() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    class SampleSignal:
        def __init__(self) -> None:
            self.receivers = []

        def connect(self, receiver, *, weak: bool) -> None:
            self.receivers.append(receiver)

        def send(self, event: object) -> None:
            for receiver in self.receivers:
                receiver("test-service", event=event)

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result
        route_kind: str
        duration_seconds: float

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="sample_events_total",
                    description="Sample events.",
                    label_fields={"result": "result", "route_kind": "route_kind"},
                ),
                HistogramMetricSpec(
                    name="sample_duration_seconds",
                    description="Sample duration.",
                    label_fields={"result": "result"},
                    value_field="duration_seconds",
                ),
            )

    @dataclass(frozen=True)
    class OtherEvent:
        result: Result

    event = SampleEvent(result=Result.SUCCESS, route_kind="list", duration_seconds=0.5)
    other_event = OtherEvent(result=Result.SUCCESS)
    signal = SampleSignal()
    counter_metric = object()
    histogram_metric = object()
    counter_calls = []
    histogram_calls = []
    counter_spec = metric_spec_for(SampleEvent, CounterMetricSpec)
    histogram_spec = metric_spec_for(SampleEvent, HistogramMetricSpec)

    def record_counter(
        metric: object, labels: dict[str, str], recorded_event: object
    ) -> None:
        counter_calls.append((metric, labels, recorded_event))

    def record_histogram(
        metric: object, labels: dict[str, str], value: float, recorded_event: object
    ) -> None:
        histogram_calls.append((metric, labels, value, recorded_event))

    connect_counter(
        signal,
        counter_metric,
        {"service_name": "sample-service"},
        SampleEvent,
        spec=counter_spec,
        record=record_counter,
    )
    connect_histogram(
        signal,
        histogram_metric,
        {"service_name": "sample-service"},
        SampleEvent,
        value_field="duration_seconds",
        spec=histogram_spec,
        record=record_histogram,
    )

    signal.send(other_event)
    signal.send(event)

    assert counter_calls == [
        (
            counter_metric,
            {
                "service_name": "sample-service",
                "result": "success",
                "route_kind": "list",
            },
            event,
        )
    ]
    assert histogram_calls == [
        (
            histogram_metric,
            {"service_name": "sample-service", "result": "success"},
            0.5,
            event,
        )
    ]


def test_metric_specs_drive_metric_connector_dispatch() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    class SampleSignal:
        def __init__(self) -> None:
            self.receivers = []

        def connect(self, receiver, *, weak: bool) -> None:
            self.receivers.append(receiver)

        def send(self, event: object) -> None:
            for receiver in self.receivers:
                receiver("test-service", event=event)

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result
        route_kind: str
        duration_seconds: float

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="sample_events_total",
                    description="Sample events.",
                    label_fields={"result": "result", "route_kind": "route_kind"},
                ),
                HistogramMetricSpec(
                    name="sample_duration_seconds",
                    description="Sample duration.",
                    label_fields={"result": "result"},
                    value_field="duration_seconds",
                ),
            )

    event = SampleEvent(result=Result.SUCCESS, route_kind="list", duration_seconds=0.5)
    signal = SampleSignal()
    counter_metric = object()
    histogram_metric = object()
    counter_calls = []
    histogram_calls = []
    counter_spec = metric_spec_for(SampleEvent, CounterMetricSpec)
    histogram_spec = metric_spec_for(SampleEvent, HistogramMetricSpec)

    def record_counter(
        metric: object, labels: dict[str, str], recorded_event: object
    ) -> None:
        counter_calls.append((metric, labels, recorded_event))

    def record_histogram(
        metric: object, labels: dict[str, str], value: float, recorded_event: object
    ) -> None:
        histogram_calls.append((metric, labels, value, recorded_event))

    assert counter_spec.name == "sample_events_total"
    assert histogram_spec.value_field == "duration_seconds"
    assert metric_label_names_for_spec(counter_spec) == ("result", "route_kind")
    assert metric_label_names_for_spec(histogram_spec) == ("result",)

    connect_metric(
        signal,
        counter_metric,
        {"service_name": "sample-service"},
        SampleEvent,
        counter_spec,
        counter_record=record_counter,
    )
    connect_metric(
        signal,
        histogram_metric,
        {"service_name": "sample-service"},
        SampleEvent,
        histogram_spec,
        histogram_record=record_histogram,
    )
    signal.send(event)

    assert counter_calls == [
        (
            counter_metric,
            {
                "service_name": "sample-service",
                "result": "success",
                "route_kind": "list",
            },
            event,
        )
    ]
    assert histogram_calls == [
        (
            histogram_metric,
            {"service_name": "sample-service", "result": "success"},
            0.5,
            event,
        )
    ]


def test_metric_specs_create_and_connect_prometheus_metrics() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    class SampleSignal:
        def __init__(self) -> None:
            self.receivers = []

        def connect(self, receiver, *, weak: bool) -> None:
            self.receivers.append(receiver)

        def send(self, event: object) -> None:
            for receiver in self.receivers:
                receiver("test-service", event=event)

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result
        route_kind: str
        duration_seconds: float

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="auto_sample_events_total",
                    description="Sample events.",
                    label_fields={"result": "result", "route_kind": "route_kind"},
                ),
                HistogramMetricSpec(
                    name="auto_sample_duration_seconds",
                    description="Sample duration.",
                    label_fields={"result": "result"},
                    value_field="duration_seconds",
                ),
            )

    registry = CollectorRegistry(auto_describe=True)
    signal = SampleSignal()
    event_types = (SampleEvent,)
    metrics = create_metrics_for_events(
        registry,
        service_label_names=("service_name",),
        event_types=event_types,
    )
    connect_metrics_for_events(
        signal,
        metrics,
        {"service_name": "sample-service"},
        event_types,
    )

    signal.send(
        SampleEvent(result=Result.SUCCESS, route_kind="list", duration_seconds=0.5)
    )
    metrics_text = generate_latest(registry).decode("utf-8")

    assert "auto_sample_events_total" in metrics_text
    assert "auto_sample_duration_seconds_count" in metrics_text
    assert 'service_name="sample-service"' in metrics_text
    assert 'result="success"' in metrics_text
    assert 'route_kind="list"' in metrics_text


def test_metric_labels_for_event_uses_custom_duck_typed_method() -> None:
    class Result(StrEnum):
        SUCCESS = "success"

    @dataclass(frozen=True)
    class SampleEvent:
        result: Result

        @classmethod
        def metric_specs(cls) -> tuple[MetricSpec, ...]:
            """테스트 event가 기록할 metric spec을 반환한다."""
            return (
                CounterMetricSpec(
                    name="custom_label_events_total",
                    description="Custom label events.",
                    label_fields={"result": "result"},
                ),
            )

        def metric_labels_for(self, spec: MetricSpec) -> dict[str, str]:
            """custom label 변환을 수행한다."""
            return {"result": f"custom_{self.result.value}"}

    event = SampleEvent(result=Result.SUCCESS)
    spec = metric_spec_for(SampleEvent, CounterMetricSpec)

    assert metric_labels_for_event(event, spec) == {"result": "custom_success"}
