from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from time import perf_counter

from fastapi import FastAPI, Path, Query, Request, status
from fastapi.responses import JSONResponse, Response
from metrics import (
    ServiceIdentity,
    bounded_http_method,
    http_server_active_requests,
    http_server_request_duration_seconds,
    service_ready,
)
from prometheus_client import (
    CollectorRegistry,
    GCCollector,
    Gauge,
    PlatformCollector,
    ProcessCollector,
    generate_latest,
)
from sqlalchemy import text
from sqlalchemy.engine import Engine
from sqlalchemy.exc import SQLAlchemyError
from starlette.routing import Match


ReadinessCheck = Callable[[], str]
MetricsConfigurator = Callable[[CollectorRegistry], None]
PROMETHEUS_TEXT_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"
DEBUG_STATUS_ROUTE_DEV_ENVIRONMENTS = frozenset({"local", "dev", "test"})


class ServiceReadiness:
    def __init__(self, metric: Gauge, identity: ServiceIdentity) -> None:
        self._metric = metric
        self._labels = identity.service_labels()
        self.set(False)

    def set(self, ready: bool) -> None:
        self._metric.labels(**self._labels).set(1 if ready else 0)


def register_service_readiness(
    registry: CollectorRegistry,
    *,
    service_name: str,
    service_version: str,
    service_environment: str,
) -> ServiceReadiness:
    identity = ServiceIdentity(
        service_name=service_name,
        service_version=service_version,
        service_environment=service_environment,
    )
    return ServiceReadiness(service_ready(registry), identity)


def register_operational_handlers(
    app: FastAPI,
    *,
    service_name: str,
    service_version: str,
    service_environment: str,
    readiness_checks: Mapping[str, ReadinessCheck],
    configure_metrics: MetricsConfigurator | None = None,
    registry: CollectorRegistry | None = None,
    include_timestamp: bool = False,
    readiness_success_status: str = "ready",
    readiness_failure_status: str = "not_ready",
    include_readiness_checks: bool = True,
) -> CollectorRegistry:
    metrics_registry = register_http_metrics(
        app,
        service_name=service_name,
        service_version=service_version,
        service_environment=service_environment,
        registry=registry,
    )
    service_readiness = register_service_readiness(
        metrics_registry,
        service_name=service_name,
        service_version=service_version,
        service_environment=service_environment,
    )

    if configure_metrics is not None:
        configure_metrics(metrics_registry)

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
        service_readiness.set(is_ready)
        payload = _operational_payload(
            status=readiness_success_status if is_ready else readiness_failure_status,
            service_name=service_name,
            include_timestamp=include_timestamp,
        )
        if include_readiness_checks:
            payload["checks"] = checks

        if not is_ready:
            return JSONResponse(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content=payload
            )

        return JSONResponse(status_code=status.HTTP_200_OK, content=payload)

    @app.get("/metrics")
    def metrics() -> Response:
        return Response(
            content=render_metrics(metrics_registry),
            media_type=PROMETHEUS_TEXT_CONTENT_TYPE,
        )

    register_debug_status_route(
        app, service_name=service_name, service_environment=service_environment
    )

    return metrics_registry


def register_http_metrics(
    app: FastAPI,
    *,
    service_name: str,
    service_version: str,
    service_environment: str,
    registry: CollectorRegistry | None = None,
) -> CollectorRegistry:
    metrics_registry = registry or CollectorRegistry(auto_describe=True)
    configure_runtime_collectors(metrics_registry)
    service_identity = ServiceIdentity(
        service_name=service_name,
        service_version=service_version,
        service_environment=service_environment,
    )
    request_duration_metric = http_server_request_duration_seconds(metrics_registry)
    active_requests_metric = http_server_active_requests(metrics_registry)

    app.state.operational_metrics_registry = metrics_registry

    @app.middleware("http")
    async def collect_http_metrics(request: Request, call_next):
        started_at = perf_counter()
        status_code = "500"
        http_route = _route_template(request)
        base_http_labels = {
            **service_identity.service_labels(),
            "http_route": http_route,
            "http_route_kind": _route_kind(http_route),
            "http_request_method": bounded_http_method(request.method),
        }
        active_requests_metric.labels(**base_http_labels).inc()

        try:
            response = await call_next(request)
            status_code = str(response.status_code)
            return response
        finally:
            active_requests_metric.labels(**base_http_labels).dec()
            request_duration_metric.labels(
                **base_http_labels,
                http_response_status_code=status_code,
            ).observe(perf_counter() - started_at)

    return metrics_registry


def render_metrics(registry: CollectorRegistry) -> str:
    return generate_latest(registry).decode("utf-8")


def register_debug_status_route(
    app: FastAPI,
    *,
    service_environment: str,
    service_name: str | None = None,
    enabled: bool | None = None,
) -> None:
    if enabled is None:
        enabled = True
    if not enabled or not _is_debug_status_route_environment(service_environment):
        return

    service_key = _debug_service_key(service_name)

    @app.get("/__debug/status/{status_code}", include_in_schema=False)
    @app.get(f"/__debug/{service_key}/status/{{status_code}}", include_in_schema=False)
    def debug_status(
        status_code: int = Path(..., ge=100, le=599),
        reason: str | None = Query(default=None),
    ) -> Response:
        if status_code < 200 or status_code in {204, 304}:
            return Response(status_code=status_code)

        return JSONResponse(
            status_code=status_code,
            content={
                "status": "debug",
                "statusCode": status_code,
                "reason": reason or "forced debug response",
            },
        )


def configure_runtime_collectors(registry: CollectorRegistry) -> None:
    GCCollector(registry=registry)
    PlatformCollector(registry=registry)
    ProcessCollector(registry=registry)


def required_settings_readiness_check(
    required_values: Mapping[str, object],
) -> ReadinessCheck:
    def check() -> str:
        missing = [
            name
            for name, value in required_values.items()
            if value is None or value == ""
        ]
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
    # route template 탐색
    # - 배경: middleware 실행 시점에는 scope["route"]가 비어 있을 수 있음
    # - 1순위: FULL match route
    # - 2순위: PARTIAL match route
    # - fallback: unmatched, raw path 사용 금지
    for route in request.app.routes:
        match, _ = route.matches(request.scope)
        if match is Match.FULL:
            return str(getattr(route, "path", "unmatched"))
    for route in request.app.routes:
        match, _ = route.matches(request.scope)
        if match is Match.PARTIAL:
            return str(getattr(route, "path", "unmatched"))
    return "unmatched"


def _route_kind(route: str) -> str:
    if route in {"/health", "/healthz", "/readyz", "/metrics"}:
        return "probe"
    if route.startswith(("/debug", "/_debug", "/__debug")):
        return "debug"
    if route == "unmatched":
        return "unmatched"
    return "api"


def _is_debug_status_route_environment(service_environment: str) -> bool:
    normalized = service_environment.strip().lower()
    return normalized in DEBUG_STATUS_ROUTE_DEV_ENVIRONMENTS


def _debug_service_key(service_name: str | None) -> str:
    if service_name is None:
        return "service"
    return service_name.removesuffix("-service")


def _run_readiness_checks(
    readiness_checks: Mapping[str, ReadinessCheck],
) -> dict[str, str]:
    checks: dict[str, str] = {}
    for name, readiness_check in readiness_checks.items():
        try:
            checks[name] = readiness_check()
        except Exception as exc:
            checks[name] = f"failed: {exc.__class__.__name__}"
    return checks


def _operational_payload(
    *, status: str, service_name: str, include_timestamp: bool
) -> dict[str, object]:
    payload: dict[str, object] = {"status": status, "service": service_name}
    if include_timestamp:
        payload["timestamp"] = datetime.now(UTC).isoformat()
    return payload
