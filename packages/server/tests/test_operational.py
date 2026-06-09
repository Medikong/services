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


def register_test_operational_handlers(app: FastAPI, **kwargs: object) -> None:
    register_operational_handlers(
        app,
        service_name="test-service",
        service_version="test-version",
        service_environment="test",
        **kwargs,
    )


def test_register_operational_handlers_adds_healthz_readyz_and_metrics() -> None:
    app = FastAPI()
    register_test_operational_handlers(app, readiness_checks={"database": lambda: "ok"})
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
    register_test_operational_handlers(
        app,
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
    register_test_operational_handlers(app, readiness_checks={"database": failing_check})
    client = TestClient(app)

    response = client.get("/readyz")

    assert response.status_code == 503
    assert response.json()["checks"] == {"database": "failed: RuntimeError"}


def test_metrics_returns_prometheus_text_and_http_metrics() -> None:
    app = FastAPI()
    register_test_operational_handlers(app, readiness_checks={})
    client = TestClient(app)
    client.get("/healthz")

    response = client.get("/metrics")

    assert response.status_code == 200
    assert response.headers["content-type"].startswith("text/plain; version=0.0.4")
    assert "http_server_request_duration_seconds" in response.text
    assert "http_server_active_requests" in response.text
    assert "service_ready" in response.text
    assert 'http_route="/healthz"' in response.text
    assert 'http_request_method="GET"' in response.text
    assert 'http_response_status_code="200"' in response.text
    assert 'service_name="test-service"' in response.text
    assert 'service_version="test-version"' in response.text
    assert 'service_environment="test"' in response.text


def test_http_metrics_use_route_template_not_raw_path() -> None:
    app = FastAPI()

    @app.get("/orders/{order_id}")
    def read_order(order_id: str) -> dict[str, str]:
        return {"order_id": order_id}

    register_operational_handlers(
        app,
        service_name="test-service",
        readiness_checks={},
        service_version="2026.06.09.1",
        service_environment="aws-dev",
    )
    client = TestClient(app)
    client.get("/orders/ord-123")

    response = client.get("/metrics")

    assert 'http_route="/orders/{order_id}"' in response.text
    assert "/orders/ord-123" not in response.text
    assert 'service_version="2026.06.09.1"' in response.text
    assert 'service_environment="aws-dev"' in response.text


def test_http_metrics_do_not_expose_high_cardinality_labels() -> None:
    app = FastAPI()
    register_test_operational_handlers(app, readiness_checks={})
    client = TestClient(app)
    client.get("/healthz", headers={"X-Request-Id": "req-123"})

    response = client.get("/metrics")

    forbidden_labels = (
        "request_id",
        "trace_id",
        "span_id",
        "correlation_id",
        "user_id",
        "payment_id",
        "reservation_id",
        "ticket_id",
        "path",
    )
    for label in forbidden_labels:
        assert f"{label}=" not in response.text


def test_operational_handlers_require_service_identity_values() -> None:
    app = FastAPI()

    try:
        register_operational_handlers(
            app,
            service_name="test-service",
            service_version="",
            service_environment="test",
            readiness_checks={},
        )
    except ValueError as exc:
        assert "service_version" in str(exc)
    else:
        raise AssertionError("service_version must be required")


def test_metrics_configurator_can_register_service_specific_metrics() -> None:
    def configure_metrics(registry: CollectorRegistry) -> None:
        business_gauge = Gauge(
            "ticketing_business_value",
            "Service-specific business metric owned by the service.",
            registry=registry,
        )
        business_gauge.set(7)

    app = FastAPI()
    register_test_operational_handlers(
        app,
        readiness_checks={},
        configure_metrics=configure_metrics,
    )
    client = TestClient(app)

    response = client.get("/metrics")

    assert response.status_code == 200
    assert "ticketing_business_value 7.0" in response.text


def test_operational_handlers_can_preserve_legacy_ready_status_without_checks() -> None:
    app = FastAPI()
    register_test_operational_handlers(
        app,
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
    register_test_operational_handlers(
        app,
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
