from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from time import perf_counter

from fastapi import FastAPI, Request, status
from fastapi.responses import JSONResponse, Response
from prometheus_client import (
    CollectorRegistry,
    Counter,
    Gauge,
    GCCollector,
    Histogram,
    PlatformCollector,
    ProcessCollector,
    generate_latest,
)
from sqlalchemy import text
from sqlalchemy.engine import Engine
from sqlalchemy.exc import SQLAlchemyError


ReadinessCheck = Callable[[], str]
MetricsConfigurator = Callable[[CollectorRegistry], None]
PROMETHEUS_TEXT_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"


def register_operational_handlers(
    app: FastAPI,
    *,
    service_name: str,
    readiness_checks: Mapping[str, ReadinessCheck],
    configure_metrics: MetricsConfigurator | None = None,
    registry: CollectorRegistry | None = None,
    include_timestamp: bool = False,
    readiness_success_status: str = "ready",
    readiness_failure_status: str = "not_ready",
    include_readiness_checks: bool = True,
) -> CollectorRegistry:
    metrics_registry = registry or CollectorRegistry(auto_describe=True)
    configure_runtime_collectors(metrics_registry)

    http_requests_total = Counter(
        "http_requests_total",
        "Total HTTP requests.",
        ["service", "method", "path", "status"],
        registry=metrics_registry,
    )
    http_request_duration_seconds = Histogram(
        "http_request_duration_seconds",
        "HTTP request duration in seconds.",
        ["service", "method", "path"],
        registry=metrics_registry,
    )
    service_ready = Gauge(
        "service_ready",
        "Service readiness state. Ready is 1, not ready is 0.",
        ["service"],
        registry=metrics_registry,
    )

    if configure_metrics is not None:
        configure_metrics(metrics_registry)

    app.state.operational_metrics_registry = metrics_registry

    @app.middleware("http")
    async def collect_http_metrics(request: Request, call_next):
        started_at = perf_counter()
        status_code = "500"

        try:
            response = await call_next(request)
            status_code = str(response.status_code)
            return response
        finally:
            path = _route_template(request)
            duration = perf_counter() - started_at
            http_requests_total.labels(
                service=service_name,
                method=request.method,
                path=path,
                status=status_code,
            ).inc()
            http_request_duration_seconds.labels(
                service=service_name,
                method=request.method,
                path=path,
            ).observe(duration)

    @app.get("/healthz")
    def healthz() -> dict[str, object]:
        return _operational_payload(
            status="ok",
            service_name=service_name,
            include_timestamp=include_timestamp,
        )

    @app.get("/readyz")
    def readyz() -> JSONResponse:
        checks = _run_readiness_checks(readiness_checks)
        is_ready = all(result == "ok" for result in checks.values())
        service_ready.labels(service=service_name).set(1 if is_ready else 0)
        payload = _operational_payload(
            status=readiness_success_status if is_ready else readiness_failure_status,
            service_name=service_name,
            include_timestamp=include_timestamp,
        )
        if include_readiness_checks:
            payload["checks"] = checks

        if not is_ready:
            return JSONResponse(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content=payload)

        return JSONResponse(status_code=status.HTTP_200_OK, content=payload)

    @app.get("/metrics")
    def metrics() -> Response:
        return Response(content=generate_latest(metrics_registry), media_type=PROMETHEUS_TEXT_CONTENT_TYPE)

    return metrics_registry


def configure_runtime_collectors(registry: CollectorRegistry) -> None:
    GCCollector(registry=registry)
    PlatformCollector(registry=registry)
    ProcessCollector(registry=registry)


def required_settings_readiness_check(required_values: Mapping[str, object]) -> ReadinessCheck:
    def check() -> str:
        missing = [name for name, value in required_values.items() if value is None or value == ""]
        if missing:
            return f"failed: missing required setting: {', '.join(missing)}"
        return "ok"

    return check


def sqlalchemy_readiness_check(engine: Engine) -> ReadinessCheck:
    def check() -> str:
        try:
            with engine.connect() as connection:
                connection.execute(text("SELECT 1"))
        except SQLAlchemyError as exc:
            return f"failed: {exc.__class__.__name__}"
        return "ok"

    return check


def _route_template(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return str(path or request.url.path)


def _run_readiness_checks(readiness_checks: Mapping[str, ReadinessCheck]) -> dict[str, str]:
    checks: dict[str, str] = {}
    for name, readiness_check in readiness_checks.items():
        try:
            checks[name] = readiness_check()
        except Exception as exc:
            checks[name] = f"failed: {exc.__class__.__name__}"
    return checks


def _operational_payload(*, status: str, service_name: str, include_timestamp: bool) -> dict[str, object]:
    payload: dict[str, object] = {"status": status, "service": service_name}
    if include_timestamp:
        payload["timestamp"] = datetime.now(UTC).isoformat()
    return payload
