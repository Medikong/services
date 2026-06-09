from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from time import perf_counter

from fastapi import FastAPI, Request, status
from fastapi.responses import JSONResponse, Response
from metrics import (
    ServiceIdentity,
    http_server_active_requests,
    http_server_request_duration_seconds,
    service_ready,
)
from prometheus_client import (
    CollectorRegistry,
    GCCollector,
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
    metrics_registry = registry or CollectorRegistry(auto_describe=True)
    configure_runtime_collectors(metrics_registry)
    # 서비스 식별 label
    # - 적용 대상: 모든 공통 HTTP/readiness 메트릭
    # - 입력 기준: 호출자가 외부 설정/env를 해석해 넘긴 값만 사용
    # - 실패 기준: 누락/빈 값은 잘못된 운영 설정으로 보고 즉시 예외
    service_identity = ServiceIdentity(
        service_name=service_name,
        service_version=service_version,
        service_environment=service_environment,
    )

    # Prometheus metric handles
    # - 생성자 위치: packages/metrics
    # - 이 모듈 책임: 요청별 label 값 계산과 metric 기록
    # - 범위: P0 공통 HTTP/readiness metric
    request_duration_metric = http_server_request_duration_seconds(metrics_registry)
    active_requests_metric = http_server_active_requests(metrics_registry)
    service_ready_metric = service_ready(metrics_registry)
    service_ready_metric.labels(**service_identity.service_labels()).set(0)

    if configure_metrics is not None:
        configure_metrics(metrics_registry)

    app.state.operational_metrics_registry = metrics_registry

    @app.middleware("http")
    async def collect_http_metrics(request: Request, call_next):
        started_at = perf_counter()
        status_code = "500"
        # http_route label
        # - 사용: FastAPI route template
        # - 예시: /payments/{payment_id}
        # - 금지: /payments/pay-123 같은 raw URL path
        http_route = _route_template(request)
        base_http_labels = {
            **service_identity.service_labels(),
            "http_route": http_route,
            "http_request_method": request.method,
        }
        active_requests_metric.labels(**base_http_labels).inc()

        try:
            response = await call_next(request)
            status_code = str(response.status_code)
            return response
        finally:
            duration = perf_counter() - started_at
            # active request gauge
            # - 증가: 요청 처리 시작
            # - 감소: 응답/예외와 무관하게 finally에서 보장
            # - 목적: 느린 요청, downstream 대기, handler hang 감지
            active_requests_metric.labels(**base_http_labels).dec()
            request_duration_metric.labels(
                **base_http_labels,
                http_response_status_code=status_code,
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
        service_ready_metric.labels(**service_identity.service_labels()).set(1 if is_ready else 0)
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
