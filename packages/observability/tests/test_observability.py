from collections.abc import Iterator
from contextlib import contextmanager
import json
import logging
import sys
import types
from uuid import UUID, uuid5

from errors import in_domain
from fastapi import FastAPI
from fastapi.testclient import TestClient
import pytest
from middleware import (
    RequestContextMiddleware,
    ResponseHeadersMiddleware,
    RuntimeRecoveryMiddleware,
    request_context_middleware_options,
)

from observability import error_context as error_context_module
from observability import callsite as callsite_module
from observability import database as database_module
from observability import exceptions as exceptions_module
from observability import fastapi as fastapi_module
from observability import (
    CALLSITE_MODULE_PREFIXES_ENV,
    Callsite,
    CallsiteSpanProcessor,
    DOMAIN_REJECTION_OBSERVATION,
    OBSERVABILITY_ENV_KEYS,
    DEFAULT_CALLSITE_MODULE_PREFIXES,
    DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS,
    ErrorObservation,
    HttpError,
    ObservabilityConfig,
    NoopTraceRecorder,
    ProfilingConfig,
    TraceContext,
    capture_current_trace_context,
    configure_process_logging,
    configure_process_observability,
    configure_process_profiling,
    configure_process_tracing,
    create_request_log_middleware,
    clear_callsite_cache,
    get_callsite,
    instrument_fastapi_app,
    list_callsites,
    observability_config_from_env,
    put_callsite,
    record_exception,
    start_trace_span,
    trace_recorder,
)
from observability import process as process_module
from observability import profiling as profiling_module
from observability import tracing as tracing_module
from observability.tracing import _otlp_trace_export_enabled


TEST_UUID_NAMESPACE = UUID("018f0d5b-8e30-7a60-9bf1-91b6d979d3c0")
SEAT_ID = str(uuid5(TEST_UUID_NAMESPACE, "observability-test:seat:1"))


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
            CALLSITE_MODULE_PREFIXES_ENV: " app, worker, , domain ",
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
        callsite_module_prefixes=("app", "worker", "domain"),
        profiling=ProfilingConfig(
            application_name="test-service",
            tags={"service": "test-service", "environment": "staging", "version": "1.2.3"},
        ),
    )
    assert set(OBSERVABILITY_ENV_KEYS) == {
        "SERVICE_VERSION",
        "SERVICE_ENVIRONMENT",
        "OTEL_SDK_DISABLED",
        "OTEL_TRACES_EXPORTER",
        "OTEL_EXPORTER_OTLP_ENDPOINT",
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
        "OTEL_PYTHON_FASTAPI_EXCLUDED_URLS",
        CALLSITE_MODULE_PREFIXES_ENV,
        "PYROSCOPE_ENABLED",
        "PYROSCOPE_SERVER_ADDRESS",
        "PYROSCOPE_APPLICATION_NAME",
        "PYROSCOPE_SAMPLE_RATE",
        "PYROSCOPE_SPAN_PROFILES_ENABLED",
        "PYROSCOPE_ONCPU",
        "PYROSCOPE_GIL_ONLY",
        "PYROSCOPE_TAGS",
        "PYROSCOPE_BASIC_AUTH_USERNAME",
        "PYROSCOPE_BASIC_AUTH_PASSWORD",
        "PYROSCOPE_TENANT_ID",
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
    assert config.callsite_module_prefixes == DEFAULT_CALLSITE_MODULE_PREFIXES


def test_observability_config_uses_default_callsite_prefixes_when_env_is_blank() -> None:
    config = observability_config_from_env(
        "test-service",
        env={CALLSITE_MODULE_PREFIXES_ENV: " ,  , "},
    )

    assert config.callsite_module_prefixes == DEFAULT_CALLSITE_MODULE_PREFIXES


def test_observability_config_reads_fastapi_trace_exclusions_from_env() -> None:
    config = observability_config_from_env(
        "test-service",
        env={"OTEL_PYTHON_FASTAPI_EXCLUDED_URLS": "/livez, /readyz ,/internal/metrics"},
    )

    assert config.fastapi_trace_excluded_urls == ("/livez", "/readyz", "/internal/metrics")


def test_observability_config_reads_pyroscope_settings_from_env() -> None:
    config = observability_config_from_env(
        "auth-service",
        env={
            "SERVICE_VERSION": "abc123",
            "SERVICE_ENVIRONMENT": "aws-dev",
            "PYROSCOPE_ENABLED": "true",
            "PYROSCOPE_SERVER_ADDRESS": "http://pyroscope:4040",
            "PYROSCOPE_APPLICATION_NAME": "medikong.auth",
            "PYROSCOPE_SAMPLE_RATE": "50",
            "PYROSCOPE_SPAN_PROFILES_ENABLED": "true",
            "PYROSCOPE_ONCPU": "false",
            "PYROSCOPE_GIL_ONLY": "false",
            "PYROSCOPE_TAGS": "scenario=reservation-journey-load-test, run_id=run-001",
            "PYROSCOPE_BASIC_AUTH_USERNAME": "profiles-user",
            "PYROSCOPE_BASIC_AUTH_PASSWORD": "profiles-password",
            "PYROSCOPE_TENANT_ID": "tenant-a",
        },
    )

    assert config.profiling == ProfilingConfig(
        enabled=True,
        server_address="http://pyroscope:4040",
        application_name="medikong.auth",
        sample_rate=50,
        span_profiles_enabled=True,
        oncpu=False,
        gil_only=False,
        tags={
            "service": "auth-service",
            "environment": "aws-dev",
            "version": "abc123",
            "scenario": "reservation-journey-load-test",
            "run_id": "run-001",
        },
        basic_auth_username="profiles-user",
        basic_auth_password="profiles-password",
        tenant_id="tenant-a",
    )


@pytest.mark.parametrize("tag_key", ["user_id", "reservation_id", "payment_id", "ticket_id", "customer_id"])
def test_observability_config_rejects_high_cardinality_pyroscope_tags(tag_key: str) -> None:
    with pytest.raises(ValueError, match="forbidden high-cardinality tag key"):
        observability_config_from_env(
            "auth-service",
            env={"PYROSCOPE_TAGS": f"{tag_key}=123"},
        )


def test_configure_process_profiling_skips_disabled_config(monkeypatch) -> None:
    monkeypatch.setattr(profiling_module, "_profiling_configured", False)

    configured = configure_process_profiling(
        ObservabilityConfig(
            service_name="auth-service",
            profiling=ProfilingConfig(enabled=False, server_address="http://pyroscope:4040"),
        )
    )

    assert not configured


def test_configure_process_profiling_configures_pyroscope_once(monkeypatch) -> None:
    calls: list[dict[str, object]] = []
    fake_pyroscope_module = types.SimpleNamespace(configure=lambda **kwargs: calls.append(kwargs))
    monkeypatch.setitem(sys.modules, "pyroscope", fake_pyroscope_module)
    monkeypatch.setattr(profiling_module, "_profiling_configured", False)

    config = ObservabilityConfig(
        service_name="auth-service",
        profiling=ProfilingConfig(
            enabled=True,
            server_address="http://pyroscope:4040",
            application_name="medikong.auth",
            sample_rate=50,
            oncpu=True,
            gil_only=True,
            tags={"service": "auth-service", "scenario": "reservation-journey-load-test"},
            basic_auth_username="profiles-user",
            basic_auth_password="profiles-password",
            tenant_id="tenant-a",
        ),
    )

    assert configure_process_profiling(config)
    assert not configure_process_profiling(config)
    assert calls == [
        {
            "application_name": "medikong.auth",
            "server_address": "http://pyroscope:4040",
            "sample_rate": 50,
            "oncpu": True,
            "gil_only": True,
            "tags": {"service": "auth-service", "scenario": "reservation-journey-load-test"},
            "basic_auth_username": "profiles-user",
            "basic_auth_password": "profiles-password",
            "tenant_id": "tenant-a",
        }
    ]


def test_configure_process_profiling_requires_server_address(monkeypatch) -> None:
    monkeypatch.setattr(profiling_module, "_profiling_configured", False)

    with pytest.raises(ValueError, match="PYROSCOPE_SERVER_ADDRESS is required"):
        configure_process_profiling(
            ObservabilityConfig(
                service_name="auth-service",
                profiling=ProfilingConfig(enabled=True),
            )
        )


def test_configure_process_observability_wires_process_level_hooks(monkeypatch) -> None:
    calls: list[tuple[str, str | None]] = []

    def fake_configure_process_logging() -> None:
        calls.append(("logging", None))

    def fake_configure_process_profiling(config: ObservabilityConfig) -> bool:
        calls.append(("profiling", config.service_name))
        return True

    def fake_configure_process_tracing(config: ObservabilityConfig) -> None:
        calls.append(("tracing", config.service_name))

    monkeypatch.setattr(process_module, "configure_process_logging", fake_configure_process_logging)
    monkeypatch.setattr(process_module, "configure_process_profiling", fake_configure_process_profiling)
    monkeypatch.setattr(process_module, "configure_process_tracing", fake_configure_process_tracing)

    configure_process_observability(ObservabilityConfig(service_name="worker-service"))

    assert calls == [
        ("logging", None),
        ("profiling", "worker-service"),
        ("tracing", "worker-service"),
    ]


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
            self.span_processors: list[object] = []

        def add_span_processor(self, processor: object) -> None:
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
    assert isinstance(providers[0].span_processors[0], CallsiteSpanProcessor)
    assert isinstance(providers[0].span_processors[1], FakeBatchSpanProcessor)


def test_configure_tracing_skips_pyroscope_span_processor_when_disabled(monkeypatch) -> None:
    providers: list[object] = []
    span_processors: list[object] = []

    class FakeTracerProvider:
        def __init__(self, *, resource: object) -> None:
            self.resource = resource
            self.span_processors = span_processors

        def add_span_processor(self, processor: object) -> None:
            self.span_processors.append(processor)

    def fake_pyroscope_span_processor() -> object:
        raise AssertionError("pyroscope span processor should not be created")

    monkeypatch.setattr(tracing_module, "_tracing_configured", False)
    monkeypatch.setattr(tracing_module, "TracerProvider", FakeTracerProvider)
    monkeypatch.setattr(tracing_module, "_pyroscope_span_processor", fake_pyroscope_span_processor)
    monkeypatch.setattr(tracing_module.trace, "set_tracer_provider", providers.append)

    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            profiling=ProfilingConfig(enabled=True, span_profiles_enabled=False),
        )
    )

    assert providers
    assert len(span_processors) == 1
    assert isinstance(span_processors[0], CallsiteSpanProcessor)


def test_configure_tracing_adds_pyroscope_span_processor_when_enabled(monkeypatch) -> None:
    providers: list[object] = []
    pyroscope_processor = object()

    class FakeTracerProvider:
        def __init__(self, *, resource: object) -> None:
            self.resource = resource
            self.span_processors: list[object] = []

        def add_span_processor(self, processor: object) -> None:
            self.span_processors.append(processor)

    monkeypatch.setattr(tracing_module, "_tracing_configured", False)
    monkeypatch.setattr(tracing_module, "TracerProvider", FakeTracerProvider)
    monkeypatch.setattr(tracing_module, "_pyroscope_span_processor", lambda: pyroscope_processor)
    monkeypatch.setattr(tracing_module.trace, "set_tracer_provider", providers.append)

    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            profiling=ProfilingConfig(enabled=True, span_profiles_enabled=True),
        )
    )
    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            profiling=ProfilingConfig(enabled=True, span_profiles_enabled=True),
        )
    )

    assert providers
    assert providers[0].span_processors[1] is pyroscope_processor
    assert len(providers[0].span_processors) == 2
    assert len(providers) == 1


def test_configure_tracing_skips_pyroscope_span_processor_when_profiling_off(monkeypatch) -> None:
    providers: list[object] = []

    class FakeTracerProvider:
        def __init__(self, *, resource: object) -> None:
            self.resource = resource
            self.span_processors: list[object] = []

        def add_span_processor(self, processor: object) -> None:
            self.span_processors.append(processor)

    def fake_pyroscope_span_processor() -> object:
        raise AssertionError("pyroscope span processor should not be created")

    monkeypatch.setattr(tracing_module, "_tracing_configured", False)
    monkeypatch.setattr(tracing_module, "TracerProvider", FakeTracerProvider)
    monkeypatch.setattr(tracing_module, "_pyroscope_span_processor", fake_pyroscope_span_processor)
    monkeypatch.setattr(tracing_module.trace, "set_tracer_provider", providers.append)

    configure_process_tracing(
        ObservabilityConfig(
            service_name="test-service",
            profiling=ProfilingConfig(enabled=False, span_profiles_enabled=True),
        )
    )

    assert providers
    assert len(providers[0].span_processors) == 1
    assert isinstance(providers[0].span_processors[0], CallsiteSpanProcessor)


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


def test_start_trace_span_starts_span_without_current_parent(monkeypatch) -> None:
    started_spans: list[tuple[str, dict[str, object] | None]] = []
    entered: list[str] = []

    class FakeTracer:
        def start_as_current_span(self, name: str, *, attributes: dict[str, object] | None = None):
            started_spans.append((name, attributes))

            @contextmanager
            def span() -> Iterator[None]:
                entered.append("enter")
                yield
                entered.append("exit")

            return span()

    monkeypatch.setattr(tracing_module.trace, "get_tracer", lambda name: FakeTracer())

    with start_trace_span("payment.outbox.dispatch_pending", {"app.component": "payment_outbox_dispatcher"}):
        entered.append("inside")

    assert started_spans == [
        ("payment.outbox.dispatch_pending", {"app.component": "payment_outbox_dispatcher"})
    ]
    assert entered == ["enter", "inside", "exit"]


def test_callsite_cache_stores_structured_values() -> None:
    clear_callsite_cache()
    callsite = Callsite(
        namespace="app.services.payment_events",
        function_name="dispatch_pending",
        file_path="/service/services/payment-service/app/services/payment_events.py",
        line_number=73,
    )

    put_callsite("payment.outbox.dispatch_pending", callsite)

    assert get_callsite("payment.outbox.dispatch_pending") == callsite
    assert list_callsites() == {"payment.outbox.dispatch_pending": callsite}
    assert callsite.location() == (
        "app.services.payment_events.dispatch_pending "
        "/service/services/payment-service/app/services/payment_events.py:73"
    )
    assert callsite.as_trace_attributes() == {
        "code.function.name": "dispatch_pending",
        "code.location": (
            "app.services.payment_events.dispatch_pending "
            "/service/services/payment-service/app/services/payment_events.py:73"
        ),
    }


def test_callsite_cache_rejects_empty_keys() -> None:
    clear_callsite_cache()

    with pytest.raises(ValueError, match="callsite key must not be empty"):
        get_callsite("")


def test_find_application_callsite_accepts_app_service_module() -> None:
    callsite = _callsite_from_module("app.services.payment_events")

    assert callsite is not None
    assert callsite.namespace == "app.services.payment_events"
    assert callsite.function_name == "locate_callsite"


def test_find_application_callsite_accepts_app_module_itself() -> None:
    callsite = _callsite_from_module("app")

    assert callsite is not None
    assert callsite.namespace == "app"
    assert callsite.function_name == "locate_callsite"


@pytest.mark.parametrize("module_name", ["__main__", "uvicorn.main", "starlette.responses"])
def test_find_application_callsite_rejects_non_app_modules(module_name: str) -> None:
    assert _callsite_from_module(module_name) is None


def test_find_application_callsite_accepts_configured_module_prefix() -> None:
    config = observability_config_from_env(
        "test-service",
        env={CALLSITE_MODULE_PREFIXES_ENV: "app, worker, domain"},
    )

    callsite = _callsite_from_module(
        "worker.jobs.dispatcher",
        module_prefixes=config.callsite_module_prefixes,
    )

    assert callsite is not None
    assert callsite.namespace == "worker.jobs.dispatcher"
    assert callsite.function_name == "locate_callsite"


def test_callsite_span_processor_sets_app_callsite_attributes() -> None:
    span = FakeSpan()
    processor = CallsiteSpanProcessor()

    _start_span_for_callsite_processor("app.services.payment_events", processor, span)

    assert span.attributes["code.function.name"] == "start_span"
    assert span.attributes["code.location"].startswith("app.services.payment_events.start_span ")
    assert "code.namespace" not in span.attributes
    assert "code.file.path" not in span.attributes
    assert "code.line.number" not in span.attributes


def test_callsite_span_processor_skips_code_attributes_when_no_callsite() -> None:
    span = FakeSpan()
    processor = CallsiteSpanProcessor()

    _start_span_for_callsite_processor("uvicorn.main", processor, span)

    assert "code.function.name" not in span.attributes
    assert "code.location" not in span.attributes


def test_instrument_sqlalchemy_engine_registers_sqlalchemy_instrumentation(monkeypatch) -> None:
    instrumented_engines: list[object] = []

    class FakeSQLAlchemyInstrumentor:
        def instrument(self, *, engine: object) -> None:
            instrumented_engines.append(engine)

    fake_sqlalchemy_instrumentation = types.SimpleNamespace(SQLAlchemyInstrumentor=FakeSQLAlchemyInstrumentor)
    monkeypatch.setattr(database_module, "_sqlalchemy_instrumented_engine_ids", set())
    monkeypatch.setitem(sys.modules, "opentelemetry.instrumentation.sqlalchemy", fake_sqlalchemy_instrumentation)
    engine = object()

    database_module.instrument_sqlalchemy_engine(engine)

    assert instrumented_engines == [engine]


def test_instrument_sqlalchemy_pool_events_records_checkout_and_checkin(monkeypatch) -> None:
    recorder = RecordingTraceRecorder()
    listeners: list[tuple[object, str, object]] = []

    class FakePool:
        def status(self) -> str:
            return "Pool size: 5 Connections in pool: 1"

    class FakeEngine:
        pool = FakePool()

    def fake_listen(engine: object, event_name: str, listener: object) -> None:
        listeners.append((engine, event_name, listener))

    fake_sqlalchemy = types.SimpleNamespace(event=types.SimpleNamespace(listen=fake_listen))
    monkeypatch.setattr(database_module, "_sqlalchemy_pool_event_engine_ids", set())
    monkeypatch.setattr(tracing_module, "trace_recorder", lambda: recorder)
    monkeypatch.setitem(sys.modules, "sqlalchemy", fake_sqlalchemy)
    engine = FakeEngine()

    database_module.instrument_sqlalchemy_pool_events(engine)
    checkout_listener = listeners[0][2]
    checkin_listener = listeners[1][2]
    checkout_listener(None, None, None)
    checkin_listener(None, None)

    assert listeners[:2] == [
        (engine, "checkout", checkout_listener),
        (engine, "checkin", checkin_listener),
    ]
    assert [event[0] for event in recorder.events] == [
        "sqlalchemy.pool.checkout",
        "sqlalchemy.pool.checkin",
    ]
    assert recorder.events[0][1]["db.system"] == "sqlalchemy"
    assert isinstance(recorder.events[0][1]["db.pool.status"], str)


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


def test_capture_current_trace_context_keeps_carrier_and_ids(monkeypatch) -> None:
    span = FakeSpan()

    def fake_inject(carrier: dict[str, str]) -> None:
        carrier["traceparent"] = "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01"
        carrier["tracestate"] = "vendor=value"

    monkeypatch.setattr(tracing_module.trace, "get_current_span", lambda: span)
    monkeypatch.setattr(tracing_module.propagate, "inject", fake_inject)

    trace_context = capture_current_trace_context()

    assert trace_context == TraceContext(
        carrier={
            "traceparent": "00-4f3b2c1a9d8e7f60123456789abcdef0-6f1a2b3c4d5e6f70-01",
            "tracestate": "vendor=value",
        },
        trace_id="4f3b2c1a9d8e7f60123456789abcdef0",
        span_id="6f1a2b3c4d5e6f70",
    )


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


def test_request_observability_records_call_next_boundary_events(caplog, monkeypatch) -> None:
    recorder = RecordingTraceRecorder()
    monkeypatch.setattr(fastapi_module, "trace_recorder", lambda: recorder)
    app = _observed_app(ObservabilityConfig(service_name="test-service"))

    @app.get("/items")
    def get_items() -> dict[str, list[str]]:
        return {"items": []}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/items")

    assert response.status_code == 200
    assert recorder.events == [
        (
            "http.request.middleware.call_next.start",
            {
                "http.method": "GET",
                "http.middleware": "request_observability",
            },
        ),
        (
            "http.request.middleware.call_next.end",
            {
                "http.method": "GET",
                "http.middleware": "request_observability",
                "http.status_code": 200,
            },
        ),
    ]


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
    in_domain("reservation").code("reservation.conflict").with_attr("seat_id", SEAT_ID).attach(exc)

    context = error_context_module.extract_error_context(exc)

    assert context["error.code"] == "reservation.conflict"
    assert context["error.domain"] == "reservation"
    assert context["error.attr.seat_id"] == SEAT_ID
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


def _callsite_from_module(
    module_name: str,
    module_prefixes: tuple[str, ...] | None = None,
) -> Callsite | None:
    namespace = {
        "__name__": module_name,
        "find_application_callsite": callsite_module.find_application_callsite,
        "module_prefixes": module_prefixes,
    }
    exec(
        "def locate_callsite():\n"
        "    if module_prefixes is None:\n"
        "        return find_application_callsite()\n"
        "    return find_application_callsite(module_prefixes)\n",
        namespace,
    )
    return namespace["locate_callsite"]()


def _start_span_for_callsite_processor(
    module_name: str,
    processor: CallsiteSpanProcessor,
    span: "FakeSpan",
) -> None:
    namespace = {
        "__name__": module_name,
        "processor": processor,
        "span": span,
    }
    exec(
        "def start_span():\n"
        "    processor.on_start(span)\n",
        namespace,
    )
    namespace["start_span"]()


class DomainRejectionError(HttpError):
    observation: ErrorObservation = DOMAIN_REJECTION_OBSERVATION

    def __init__(self) -> None:
        super().__init__(409, "reservation.conflict", "Seat is already reserved.", domain="reservation")


class FakeSpanContext:
    def __init__(self, is_valid: bool = True) -> None:
        self.is_valid = is_valid
        self.trace_id = int("4f3b2c1a9d8e7f60123456789abcdef0", 16)
        self.span_id = int("6f1a2b3c4d5e6f70", 16)


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


class RecordingTraceRecorder:
    def __init__(self) -> None:
        self.events: list[tuple[str, dict[str, object]]] = []

    def event(self, name: str, attributes: dict[str, object] | None = None) -> None:
        self.events.append((name, attributes or {}))
