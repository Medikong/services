import json
import logging

from fastapi import FastAPI
from fastapi.testclient import TestClient
from middleware import install_runtime_middleware

from observability import (
    OBSERVABILITY_ENV_KEYS,
    ObservabilityConfig,
    configure_process_logging,
    configure_process_tracing,
    create_request_log_middleware,
    instrument_fastapi_app,
    observability_config_from_env,
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
    )
    assert set(OBSERVABILITY_ENV_KEYS) == {
        "SERVICE_VERSION",
        "SERVICE_ENVIRONMENT",
        "OTEL_SDK_DISABLED",
        "OTEL_TRACES_EXPORTER",
        "OTEL_EXPORTER_OTLP_ENDPOINT",
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
    }


def test_observability_config_from_env_falls_back_to_common_otlp_endpoint() -> None:
    config = observability_config_from_env(
        "test-service",
        env={"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317"},
    )

    assert config.otlp_trace_exporter_endpoint == "http://collector:4317"


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


def test_request_observability_emits_single_line_json_log(caplog) -> None:
    app = _observed_app(ObservabilityConfig(service_name="test-service"))

    @app.get("/items/{item_id}")
    def get_item(item_id: str) -> dict[str, str]:
        return {"itemId": item_id}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get(
        "/items/item-1",
        headers={"X-Request-Id": "req-test", "X-Client-Action-Id": "act-test"},
    )

    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "req-test"
    log = _request_log(caplog.records)
    assert log["service.name"] == "test-service"
    assert log["severity"] == "INFO"
    assert log["severity_text"] == "INFO"
    assert log["request_id"] == "req-test"
    assert log["client_action_id"] == "act-test"
    assert log["trace_id"]
    assert log["span_id"]
    assert log["http.method"] == "GET"
    assert log["http.route"] == "/items/{item_id}"
    assert log["http.status_code"] == 200
    assert isinstance(log["duration_ms"], int)


def test_request_observability_logs_failed_request_fields(caplog) -> None:
    app = _observed_app(ObservabilityConfig(service_name="test-service"))
    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/missing", headers={"X-Request-Id": "req-missing"})

    assert response.status_code == 404
    log = _request_log(caplog.records)
    assert log["request_id"] == "req-missing"
    assert log["trace_id"]
    assert log["span_id"]
    assert log["service.name"] == "test-service"
    assert log["severity"] == "INFO"
    assert log["http.status_code"] == 404
    assert isinstance(log["duration_ms"], int)


def _observed_app(config: ObservabilityConfig) -> FastAPI:
    app = FastAPI()
    configure_process_logging()
    configure_process_tracing(config)
    instrument_fastapi_app(app)
    app.middleware("http")(create_request_log_middleware(config))
    install_runtime_middleware(app)
    return app


def _request_log(records: list[logging.LogRecord]) -> dict[str, object]:
    for record in reversed(records):
        if record.name == "test-service" and record.message.startswith("{"):
            payload = json.loads(record.message)
            if payload.get("event") == "http.request.completed":
                return payload
    raise AssertionError("request JSON log was not emitted")
