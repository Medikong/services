from collections.abc import Iterator
from contextlib import contextmanager
import json
import logging
import sys
import types

from errors import in_domain
from fastapi import FastAPI
from fastapi.testclient import TestClient
from middleware import (
    RequestContextMiddleware,
    ResponseHeadersMiddleware,
    RuntimeRecoveryMiddleware,
    request_context_middleware_options,
)

from observability import error_context as error_context_module
from observability import exceptions as exceptions_module
from observability import fastapi as fastapi_module
from observability import (
    DOMAIN_REJECTION_OBSERVATION,
    OBSERVABILITY_ENV_KEYS,
    DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS,
    ErrorObservation,
    HttpError,
    ObservabilityConfig,
    NoopTraceRecorder,
    configure_process_logging,
    configure_process_tracing,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
    record_exception,
    trace_recorder,
)
from observability import tracing as tracing_module
from observability.tracing import _otlp_trace_export_enabled


def test_observability_config_from_env_maps_explicit_otel_settings() -> None:
    config = observability_config_from_env(
        "test-service",
        env={
            "SERVICE_VERSION": "1.2.3",
            "SERVICE_ENVIRONMENT": "staging",
            "OTEL_SDK_DISABLED": "true",
            "OTEL_TRACES_EXPORTER": "none",
            "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://collector:4318/v1/traces",
        },
    )

    assert config == ObservabilityConfig(
        service_name="test-service",
        service_version="1.2.3",
        service_environment="staging",
        otel_sdk_disabled=True,
        otel_traces_exporter="none",
        otlp_trace_exporter_endpoint="http://collector:4318/v1/traces",
        fastapi_trace_excluded_urls=DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS,
    )
    assert set(OBSERVABILITY_ENV_KEYS) == {
        "SERVICE_VERSION",
        "SERVICE_ENVIRONMENT",
        "OTEL_SDK_DISABLED",
        "OTEL_TRACES_EXPORTER",
        "OTEL_EXPORTER_OTLP_ENDPOINT",
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
        "OTEL_PYTHON_FASTAPI_EXCLUDED_URLS",
    }


def test_observability_config_from_env_falls_back_to_common_otlp_endpoint() -> None:
    config = observability_config_from_env(
        "test-service",
        env={"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317"},
    )

    assert config.otlp_trace_exporter_endpoint == "http://collector:4317"


def test_observability_config_defaults_common_fastapi_trace_exclusions() -> None:
    config = observability_config_from_env("test-service", env={})

    assert config.fastapi_trace_excluded_urls == ("/healthz", "/readyz", "/metrics")


def test_observability_config_reads_fastapi_trace_exclusions_from_env() -> None:
    config = observability_config_from_env(
        "test-service",
        env={"OTEL_PYTHON_FASTAPI_EXCLUDED_URLS": "/livez, /readyz ,/internal/metrics"},
    )

    assert config.fastapi_trace_excluded_urls == ("/livez", "/readyz", "/internal/metrics")


def test_instrument_fastapi_app_passes_configured_excluded_urls(monkeypatch) -> None:
    calls: list[dict[str, object]] = []

    class FakeFastAPIInstrumentor:
        @staticmethod
        def instrument_app(app: FastAPI, **kwargs: object) -> None:
            calls.append({"app": app, **kwargs})

    fake_fastapi_module = types.SimpleNamespace(FastAPIInstrumentor=FakeFastAPIInstrumentor)
    monkeypatch.setitem(sys.modules, "opentelemetry.instrumentation.fastapi", fake_fastapi_module)

    app = FastAPI()
    instrument_fastapi_app(app, ObservabilityConfig(service_name="test-service"))

    assert calls == [{"app": app, "excluded_urls": "/healthz,/readyz,/metrics"}]


def test_otlp_trace_export_enabled_only_accepts_otlp_with_endpoint() -> None:
    assert _otlp_trace_export_enabled(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="otlp",
            otlp_trace_exporter_endpoint="http://collector:4317",
        )
    )
    assert not _otlp_trace_export_enabled(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="none",
            otlp_trace_exporter_endpoint="http://collector:4317",
        )
    )
    assert not _otlp_trace_export_enabled(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="zipkin",
            otlp_trace_exporter_endpoint="http://collector:4317",
        )
    )
    assert not _otlp_trace_export_enabled(ObservabilityConfig(service_name="test-service", otel_traces_exporter="otlp"))


def test_configure_tracing_passes_explicit_otlp_trace_endpoint(monkeypatch) -> None:
    exporters: list[str | None] = []
    providers: list[object] = []

    class FakeBatchSpanProcessor:
        def __init__(self, exporter: object) -> None:
            self.exporter = exporter

    class FakeTracerProvider:
        def __init__(self, *, resource: object) -> None:
            self.resource = resource
            self.span_processors: list[FakeBatchSpanProcessor] = []

        def add_span_processor(self, processor: FakeBatchSpanProcessor) -> None:
            self.span_processors.append(processor)

    def fake_otlp_span_exporter(endpoint: str | None) -> object:
        exporters.append(endpoint)
        return object()

    monkeypatch.setattr(tracing_module, "_tracing_configured", False)
    monkeypatch.setattr(tracing_module, "BatchSpanProcessor", FakeBatchSpanProcessor)
    monkeypatch.setattr(tracing_module, "_otlp_span_exporter", fake_otlp_span_exporter)
    monkeypatch.setattr(tracing_module, "TracerProvider", FakeTracerProvider)
    monkeypatch.setattr(tracing_module.trace, "set_tracer_provider", providers.append)

    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="otlp",
            otlp_trace_exporter_endpoint="http://collector:4318/v1/traces",
        )
    )

    assert exporters == ["http://collector:4318/v1/traces"]
    assert providers


def test_configure_tracing_skips_unsupported_trace_exporter(monkeypatch) -> None:
    exporters: list[str | None] = []

    def fake_otlp_span_exporter(endpoint: str | None) -> object:
        exporters.append(endpoint)
        return object()

    monkeypatch.setattr(tracing_module, "_tracing_configured", False)
    monkeypatch.setattr(tracing_module, "_otlp_span_exporter", fake_otlp_span_exporter)
    monkeypatch.setattr(tracing_module.trace, "set_tracer_provider", lambda provider: None)

    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="zipkin",
            otlp_trace_exporter_endpoint="http://collector:4317",
        )
    )

    assert exporters == []


def test_trace_recorder_attribute_sets_current_span_attribute(monkeypatch) -> None:
    span = FakeSpan()
    monkeypatch.setattr(tracing_module.trace, "get_current_span", lambda: span)

    trace_recorder().attribute("app.use_case", "reserve_seat")

    assert span.attributes["app.use_case"] == "reserve_seat"


def test_trace_recorder_event_adds_current_span_event(monkeypatch) -> None:
    span = FakeSpan()
    monkeypatch.setattr(tracing_module.trace, "get_current_span", lambda: span)

    trace_recorder().event("seat.hold.created", {"seat.id": "A-1"})

    assert span.events == [("seat.hold.created", {"seat.id": "A-1"})]


def test_trace_recorder_span_starts_child_span(monkeypatch) -> None:
    span = FakeSpan()
    started_spans: list[tuple[str, dict[str, object] | None]] = []
    entered: list[str] = []

    class FakeTracer:
        def start_as_current_span(self, name: str, *, attributes: dict[str, object] | None = None):
            started_spans.append((name, attributes))

            @contextmanager
            def child_span() -> Iterator[None]:
                entered.append("enter")
                yield
                entered.append("exit")

            return child_span()

    monkeypatch.setattr(tracing_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(tracing_module.trace, "get_tracer", lambda name: FakeTracer())

    with trace_recorder().span("reservation.reserve_seat", {"seat.id": "A-1"}):
        entered.append("inside")

    assert started_spans == [("reservation.reserve_seat", {"seat.id": "A-1"})]
    assert entered == ["enter", "inside", "exit"]


def test_trace_recorder_noops_on_invalid_current_span(monkeypatch) -> None:
    span = FakeSpan(is_valid=False)
    monkeypatch.setattr(tracing_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(
        tracing_module.trace,
        "get_tracer",
        lambda name: (_ for _ in ()).throw(AssertionError("unexpected tracer")),
    )

    recorder = trace_recorder()
    recorder.attribute("app.use_case", "reserve_seat")
    recorder.event("seat.hold.created", {"seat.id": "A-1"})
    with recorder.span("reservation.reserve_seat"):
        pass

    assert span.attributes == {}
    assert span.events == []


def test_noop_trace_recorder_ignores_all_calls() -> None:
    recorder = NoopTraceRecorder()

    recorder.attribute("app.use_case", "reserve_seat")
    recorder.event("seat.hold.created", {"seat.id": "A-1"})
    with recorder.span("reservation.reserve_seat"):
        pass


def test_request_observability_emits_single_line_json_log(caplog, monkeypatch) -> None:
    span_attributes: dict[str, object] = {}
    monkeypatch.setattr(fastapi_module, "set_current_span_attributes", span_attributes.update)
    app = _observed_app(
        ObservabilityConfig(
            service_name="test-service",
            service_version="2026.06.11",
            service_environment="test",
        )
    )

    @app.get("/items/{item_id}")
    def get_item(item_id: str) -> dict[str, str]:
        return {"itemId": item_id}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get(
        "/items/item-1",
        headers={
            "X-Request-Id": "11111111-1111-4111-8111-111111111111",
            "X-Client-Action-Id": "22222222-2222-4222-8222-222222222222",
        },
    )

    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "11111111-1111-4111-8111-111111111111"
    log = _request_log(caplog.records)
    assert log["service.name"] == "test-service"
    assert log["service.version"] == "2026.06.11"
    assert log["service.environment"] == "test"
    assert log["severity"] == "INFO"
    assert log["severity_text"] == "INFO"
    assert log["request_id"] == "11111111-1111-4111-8111-111111111111"
    assert log["client_action_id"] == "22222222-2222-4222-8222-222222222222"
    assert log["trace_id"]
    assert log["span_id"]
    assert log["http.method"] == "GET"
    assert log["http.route"] == "/items/{item_id}"
    assert log["http.route.kind"] == "api"
    assert log["http.status_code"] == 200
    assert isinstance(log["duration_ms"], int)
    assert log["http.request.is_probe"] is False
    assert log["log.kind"] == "access"
    assert log["log.policy"] == "sample"
    assert span_attributes["request_id"] == "11111111-1111-4111-8111-111111111111"
    assert span_attributes["http.route"] == "/items/{item_id}"


def test_request_observability_logs_failed_request_fields(caplog) -> None:
    app = _observed_app(ObservabilityConfig(service_name="test-service"))
    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/missing", headers={"X-Request-Id": "33333333-3333-4333-8333-333333333333"})

    assert response.status_code == 404
    log = _request_log(caplog.records)
    assert log["request_id"] == "33333333-3333-4333-8333-333333333333"
    assert log["trace_id"]
    assert log["span_id"]
    assert log["service.name"] == "test-service"
    assert log["severity"] == "WARN"
    assert log["http.status_code"] == 404
    assert log["log.policy"] == "keep"
    assert isinstance(log["duration_ms"], int)


def test_request_observability_marks_probe_routes_for_collector_policy(caplog) -> None:
    app = _observed_app(ObservabilityConfig(service_name="test-service"))

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok"}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/healthz", headers={"X-Request-Id": "44444444-4444-4444-8444-444444444444"})

    assert response.status_code == 200
    log = _request_log(caplog.records)
    assert log["event"] == "http.request.completed"
    assert log["http.route"] == "/healthz"
    assert log["http.route.kind"] == "probe"
    assert log["http.request.is_probe"] is True
    assert log["log.policy"] == "drop"

    caplog.clear()
    response = client.get("/health", headers={"X-Request-Id": "55555555-5555-4555-8555-555555555555"})

    assert response.status_code == 200
    log = _request_log(caplog.records)
    assert log["http.route"] == "/health"
    assert log["http.route.kind"] == "probe"
    assert log["log.policy"] == "drop"


def test_request_observability_marks_slow_requests_for_collector_policy(caplog, monkeypatch) -> None:
    ticks = iter([10.0, 11.001])
    monkeypatch.setattr(fastapi_module, "perf_counter", lambda: next(ticks))
    app = _observed_app(ObservabilityConfig(service_name="test-service"))

    @app.get("/items")
    def get_items() -> dict[str, list[str]]:
        return {"items": []}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/items", headers={"X-Request-Id": "66666666-6666-4666-8666-666666666666"})

    assert response.status_code == 200
    log = _request_log(caplog.records)
    assert log["duration_ms"] == 1000
    assert log["severity"] == "WARN"
    assert log["log.policy"] == "keep"


def test_error_context_reads_errors_package_context() -> None:
    exc = RuntimeError("seat already reserved")
    in_domain("reservation").code("reservation.conflict").with_attr("seat_id", "seat-A1").attach(exc)

    context = error_context_module.extract_error_context(exc)

    assert context["error.code"] == "reservation.conflict"
    assert context["error.domain"] == "reservation"
    assert context["error.attr.seat_id"] == "seat-A1"
    assert context["error.occurred_at"]


def test_record_exception_marks_span_and_deduplicates(monkeypatch) -> None:
    span = FakeSpan()
    monkeypatch.setattr(exceptions_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(exceptions_module, "current_trace_context", lambda: ("trace-1", "span-1"))

    exc = RuntimeError("boom")
    first = record_exception(exc, service_name="test-service", attributes={"error.domain": "test"})
    second = record_exception(exc, service_name="test-service")

    assert first is True
    assert second is False
    assert span.recorded_exception is exc
    assert span.status_code == "ERROR"
    assert span.attributes["error.type"] == "RuntimeError"
    assert span.attributes["error.domain"] == "test"


def test_record_exception_preserves_system_failure_log_and_stacktrace(caplog, monkeypatch) -> None:
    configure_process_logging()
    span = FakeSpan()
    monkeypatch.setattr(exceptions_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(exceptions_module, "current_trace_context", lambda: ("trace-1", "span-1"))
    caplog.set_level(logging.INFO)

    record_exception(RuntimeError("boom"), service_name="test-service")

    log = _event_log(caplog.records, "test-service", "exception.recorded")
    assert log["severity_text"] == "ERROR"
    assert log["error.kind"] == "system_failure"
    assert log["error.type"] == "RuntimeError"
    assert "exception.stacktrace" in log
    assert span.status_code == "ERROR"
    assert span.recorded_exception is not None


def test_record_exception_uses_domain_rejection_observation_without_stacktrace(caplog, monkeypatch) -> None:
    configure_process_logging()
    span = FakeSpan()
    monkeypatch.setattr(exceptions_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(exceptions_module, "current_trace_context", lambda: ("trace-1", "span-1"))
    caplog.set_level(logging.INFO)

    exc = DomainRejectionError()
    record_exception(exc, service_name="test-service", attributes={"http.status_code": 409})

    log = _event_log(caplog.records, "test-service", "domain.rejection.recorded")
    assert log["severity_text"] == "INFO"
    assert log["error.kind"] == "domain_rejection"
    assert log["error.type"] == "DomainRejectionError"
    assert log["error.code"] == "reservation.conflict"
    assert log["http.status_code"] == 409
    assert "exception.stacktrace" not in log
    assert span.recorded_exception is None
    assert span.status_code is None
    assert span.events == [("domain.rejection.recorded", span.attributes)]


def _observed_app(config: ObservabilityConfig) -> FastAPI:
    app = FastAPI()
    configure_process_logging()
    configure_process_tracing(config)
    instrument_fastapi_app(app, config)
    app.middleware("http")(create_request_log_middleware(config))
    app.add_middleware(RuntimeRecoveryMiddleware)
    app.add_middleware(ResponseHeadersMiddleware)
    app.add_middleware(RequestContextMiddleware, **request_context_middleware_options())
    return app


def _request_log(records: list[logging.LogRecord]) -> dict[str, object]:
    for record in reversed(records):
        if record.name == "test-service" and record.message.startswith("{"):
            payload = json.loads(record.message)
            if payload.get("event") == "http.request.completed":
                return payload
    raise AssertionError("request JSON log was not emitted")


def _event_log(records: list[logging.LogRecord], logger_name: str, event: str) -> dict[str, object]:
    for record in reversed(records):
        if record.name == logger_name and record.message.startswith("{"):
            payload = json.loads(record.message)
            if payload.get("event") == event:
                return payload
    raise AssertionError(f"{event} JSON log was not emitted")


class DomainRejectionError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(409, "reservation.conflict", "Seat is already reserved.", domain="reservation")


class FakeSpanContext:
    def __init__(self, is_valid: bool = True) -> None:
        self.is_valid = is_valid


class FakeSpan:
    def __init__(self, *, is_valid: bool = True) -> None:
        self.attributes: dict[str, object] = {}
        self.events: list[tuple[str, dict[str, object] | None]] = []
        self.recorded_exception: BaseException | None = None
        self.status_code: str | None = None
        self.is_valid = is_valid

    def get_span_context(self) -> FakeSpanContext:
        return FakeSpanContext(self.is_valid)

    def record_exception(self, exc: BaseException) -> None:
        self.recorded_exception = exc

    def set_status(self, status: object) -> None:
        self.status_code = getattr(getattr(status, "status_code", None), "name", None)

    def set_attribute(self, key: str, value: object) -> None:
        self.attributes[key] = value

    def add_event(self, name: str, attributes: dict[str, object] | None = None) -> None:
        self.events.append((name, attributes))
