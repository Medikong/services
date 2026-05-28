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
