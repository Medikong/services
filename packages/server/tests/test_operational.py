import json
import logging
from typing import cast

from fastapi import FastAPI
from fastapi.testclient import TestClient
from prometheus_client import CollectorRegistry, Gauge
from sqlalchemy import create_engine
from sqlalchemy.engine import Engine
from sqlalchemy.exc import SQLAlchemyError

from server.operational import (
    register_operational_handlers,
    required_settings_readiness_check,
    sqlalchemy_readiness_check,
)
from server.observability import (
    OBSERVABILITY_ENV_KEYS,
    ObservabilityConfig,
    _otlp_trace_export_enabled,
    configure_tracing,
    observability_config_from_env,
    setup_request_observability,
)
from server import observability as observability_module


def test_register_operational_handlers_adds_healthz_readyz_and_metrics() -> None:
    app = FastAPI()
    register_operational_handlers(app, service_name="test-service", readiness_checks={"database": lambda: "ok"})
    client = TestClient(app)

    assert client.get("/healthz").json() == {"status": "ok", "service": "test-service"}
    assert client.get("/readyz").json() == {
        "status": "ready",
        "service": "test-service",
        "checks": {"database": "ok"},
    }
    assert client.get("/metrics").status_code == 200


def test_readyz_returns_503_and_failed_check_when_readiness_fails() -> None:
    app = FastAPI()
    register_operational_handlers(
        app,
        service_name="test-service",
        readiness_checks={"database": lambda: "failed: OperationalError"},
    )
    client = TestClient(app)

    response = client.get("/readyz")

    assert response.status_code == 503
    assert response.json() == {
        "status": "not_ready",
        "service": "test-service",
        "checks": {"database": "failed: OperationalError"},
    }


def test_readyz_surfaces_unexpected_check_exception_as_failed_check() -> None:
    def failing_check() -> str:
        raise RuntimeError("boom")

    app = FastAPI()
    register_operational_handlers(app, service_name="test-service", readiness_checks={"database": failing_check})
    client = TestClient(app)

    response = client.get("/readyz")

    assert response.status_code == 503
    assert response.json()["checks"] == {"database": "failed: RuntimeError"}


def test_metrics_returns_prometheus_text_and_http_metrics() -> None:
    app = FastAPI()
    register_operational_handlers(app, service_name="test-service", readiness_checks={})
    client = TestClient(app)
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_requests_total" in response.text
    assert 'path="/healthz"' in response.text


def test_metrics_configurator_can_register_service_specific_metrics() -> None:
    def configure_metrics(registry: CollectorRegistry) -> None:
        business_gauge = Gauge(
            "ticketing_business_value",
            "Service-specific business metric owned by the service.",
            registry=registry,
        )
        business_gauge.set(7)

    app = FastAPI()
    register_operational_handlers(
        app,
        service_name="test-service",
        readiness_checks={},
        configure_metrics=configure_metrics,
    )
    client = TestClient(app)

    response = client.get("/metrics")

    assert response.status_code == 200
    assert "ticketing_business_value 7.0" in response.text


def test_operational_handlers_can_preserve_legacy_ready_status_without_checks() -> None:
    app = FastAPI()
    register_operational_handlers(
        app,
        service_name="test-service",
        readiness_checks={},
        readiness_success_status="ok",
        readiness_failure_status="failed",
        include_readiness_checks=False,
    )
    client = TestClient(app)

    response = client.get("/readyz")

    assert response.status_code == 200
    assert response.json() == {"status": "ok", "service": "test-service"}


def test_operational_handlers_can_include_timestamp_for_existing_contracts() -> None:
    app = FastAPI()
    register_operational_handlers(
        app,
        service_name="test-service",
        readiness_checks={"database": lambda: "ok"},
        include_timestamp=True,
    )
    client = TestClient(app)

    health_response = client.get("/healthz")
    ready_response = client.get("/readyz")

    assert health_response.status_code == 200
    assert health_response.json()["timestamp"]
    assert ready_response.status_code == 200
    assert ready_response.json()["checks"] == {"database": "ok"}
    assert ready_response.json()["timestamp"]


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
    exporters: list[dict[str, str | None]] = []
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

    def fake_otlp_span_exporter(*, endpoint: str | None) -> object:
        exporters.append({"endpoint": endpoint})
        return object()

    monkeypatch.setattr(observability_module, "_tracing_configured", False)
    monkeypatch.setattr(observability_module, "BatchSpanProcessor", FakeBatchSpanProcessor)
    monkeypatch.setattr(observability_module, "OTLPSpanExporter", fake_otlp_span_exporter)
    monkeypatch.setattr(observability_module, "TracerProvider", FakeTracerProvider)
    monkeypatch.setattr(observability_module.trace, "set_tracer_provider", providers.append)

    configure_tracing(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="otlp",
            otlp_trace_exporter_endpoint="http://collector:4318/v1/traces",
        )
    )

    assert exporters == [{"endpoint": "http://collector:4318/v1/traces"}]
    assert providers


def test_configure_tracing_skips_unsupported_trace_exporter(monkeypatch) -> None:
    exporters: list[dict[str, str | None]] = []

    def fake_otlp_span_exporter(*, endpoint: str | None) -> object:
        exporters.append({"endpoint": endpoint})
        return object()

    monkeypatch.setattr(observability_module, "_tracing_configured", False)
    monkeypatch.setattr(observability_module, "OTLPSpanExporter", fake_otlp_span_exporter)
    monkeypatch.setattr(observability_module.trace, "set_tracer_provider", lambda provider: None)

    configure_tracing(
        ObservabilityConfig(
            service_name="test-service",
            otel_traces_exporter="zipkin",
            otlp_trace_exporter_endpoint="http://collector:4317",
        )
    )

    assert exporters == []


def test_request_observability_emits_single_line_json_log(caplog) -> None:
    app = FastAPI()
    setup_request_observability(app, ObservabilityConfig(service_name="test-service"))

    @app.get("/items/{item_id}")
    def get_item(item_id: str) -> dict[str, str]:
        return {"itemId": item_id}

    caplog.set_level(logging.INFO)
    client = TestClient(app)

    response = client.get("/items/item-1", headers={"X-Request-Id": "req-test"})

    assert response.status_code == 200
    assert response.headers["X-Request-Id"] == "req-test"
    log = _request_log(caplog.records)
    assert log["service.name"] == "test-service"
    assert log["severity"] == "INFO"
    assert log["severity_text"] == "INFO"
    assert log["request_id"] == "req-test"
    assert log["trace_id"]
    assert log["span_id"]
    assert log["http.method"] == "GET"
    assert log["http.route"] == "/items/{item_id}"
    assert log["http.status_code"] == 200
    assert isinstance(log["duration_ms"], int)


def test_request_observability_logs_failed_request_fields(caplog) -> None:
    app = FastAPI()
    setup_request_observability(app, ObservabilityConfig(service_name="test-service"))
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


def test_required_settings_readiness_check_reports_missing_values() -> None:
    check = required_settings_readiness_check({"service_name": "test-service", "database_url": ""})

    assert check() == "failed: missing required setting: database_url"


def test_sqlalchemy_readiness_check_executes_select_one() -> None:
    engine = create_engine("sqlite:///:memory:")

    assert sqlalchemy_readiness_check(engine)() == "ok"


def test_sqlalchemy_readiness_check_reports_sqlalchemy_errors() -> None:
    class FailingEngine:
        def connect(self) -> None:
            raise SQLAlchemyError("not available")

    check = sqlalchemy_readiness_check(cast(Engine, FailingEngine()))

    assert check() == "failed: SQLAlchemyError"


def _request_log(records: list[logging.LogRecord]) -> dict[str, object]:
    for record in reversed(records):
        if record.name == "test-service" and record.message.startswith("{"):
            payload = json.loads(record.message)
            if payload.get("event") == "http.request.completed":
                return payload
    raise AssertionError("request JSON log was not emitted")
