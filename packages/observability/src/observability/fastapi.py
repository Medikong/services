from collections.abc import Awaitable, Callable
from contextvars import ContextVar
from time import perf_counter
from uuid import uuid4

import structlog
from asgi_correlation_id import CorrelationIdMiddleware, correlation_id
from fastapi import FastAPI, Request
from starlette.responses import Response

from middleware import get_current_client_action_id, is_safe_request_id
from middleware import get_current_request_id as get_runtime_request_id
from observability.config import ObservabilityConfig
from observability.tracing import current_trace_context, set_current_span_attributes, trace_recorder


REQUEST_ID_HEADER = "X-Request-Id"
RequestIdMiddleware = CorrelationIdMiddleware
RequestHandler = Callable[[Request], Awaitable[Response]]
RequestMiddleware = Callable[[Request, RequestHandler], Awaitable[Response]]
PROBE_ROUTES = frozenset(("/health", "/healthz", "/readyz", "/metrics"))
SLOW_REQUEST_DURATION_MS = 1000
request_id_context: ContextVar[str | None] = ContextVar("request_id", default=None)


def get_current_request_id() -> str | None:
    return request_id_context.get()


def instrument_fastapi_app(app: FastAPI, config: ObservabilityConfig | None = None) -> None:
    # 들어오는 HTTP 요청 span은 FastAPI 계측이 자동으로 만든다.
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor

    excluded_urls = None
    if config is not None and config.fastapi_trace_excluded_urls:
        excluded_urls = ",".join(config.fastapi_trace_excluded_urls)
    FastAPIInstrumentor.instrument_app(app, excluded_urls=excluded_urls)


def request_id_middleware_options() -> dict[str, object]:
    # request_id는 trace_id와 별개로 고객 문의, 접근 로그, 클라이언트 응답 확인에 쓴다.
    return {
        "header_name": REQUEST_ID_HEADER,
        "update_request_header": True,
        "validator": _valid_request_id,
    }


def create_request_log_middleware(config: ObservabilityConfig) -> RequestMiddleware:
    logger = structlog.get_logger(config.service_name)

    async def request_observability_middleware(request: Request, call_next):
        request_id = _request_id(request)
        client_action_id = get_current_client_action_id()
        request.state.request_id = request_id
        request.state.client_action_id = client_action_id
        request_id_token = request_id_context.set(request_id)
        started_at = perf_counter()
        status_code = 500

        try:
            recorder = trace_recorder()
            recorder.event(
                "http.request.middleware.call_next.start",
                {
                    "http.method": request.method,
                    "http.middleware": "request_observability",
                },
            )
            response = await call_next(request)
            recorder.event(
                "http.request.middleware.call_next.end",
                {
                    "http.method": request.method,
                    "http.middleware": "request_observability",
                    "http.status_code": response.status_code,
                },
            )
            status_code = response.status_code
            response.headers[REQUEST_ID_HEADER] = request_id
            return response
        finally:
            route = _route_template(request)
            set_current_span_attributes({"request_id": request_id, "http.route": route})
            duration_seconds = perf_counter() - started_at
            # 로그는 stdout으로, trace는 OTLP로 나가므로 두 데이터를 이어 볼 ID를 함께 남긴다.
            trace_id, span_id = current_trace_context()
            duration_ms = int(duration_seconds * 1000)
            route_kind = _route_kind(route)
            severity_text = _request_severity(status_code, duration_ms)
            logger.info(
                "http.request.completed",
                **{
                    "service.name": config.service_name,
                    "service.version": config.service_version,
                    "service.environment": config.service_environment,
                    "severity": severity_text,
                    "severity_text": severity_text,
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "request_id": request_id,
                    "correlation_id": request_id,
                    "client_action_id": client_action_id,
                    "http.method": request.method,
                    "http.route": route,
                    "http.route.kind": route_kind,
                    "http.status_code": status_code,
                    "duration_ms": duration_ms,
                    "http.request.is_probe": route_kind == "probe",
                    "log.kind": "access",
                    "log.policy": _log_policy(route_kind, status_code, duration_ms),
                },
            )
            request_id_context.reset(request_id_token)

    return request_observability_middleware


def _request_id(request: Request) -> str:
    candidate = (
        get_runtime_request_id()
        or correlation_id.get()
        or request.headers.get(REQUEST_ID_HEADER)
        or request.headers.get("x-request-id")
    )
    if candidate is not None and _valid_request_id(candidate):
        return candidate.strip()
    return str(uuid4())


def _valid_request_id(request_id: str) -> bool:
    return is_safe_request_id(request_id)


def _route_template(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return str(path or request.url.path)


def _route_kind(route: str) -> str:
    if route in PROBE_ROUTES:
        return "probe"
    if route.startswith("/debug") or route.startswith("/_debug"):
        return "debug"
    return "api"


def _request_severity(status_code: int, duration_ms: int) -> str:
    if status_code >= 500:
        return "ERROR"
    if status_code >= 400 or duration_ms >= SLOW_REQUEST_DURATION_MS:
        return "WARN"
    return "INFO"


def _log_policy(route_kind: str, status_code: int, duration_ms: int) -> str:
    if route_kind == "probe" and status_code < 500:
        return "drop"
    if status_code >= 500 or duration_ms >= SLOW_REQUEST_DURATION_MS:
        return "keep"
    if status_code >= 400 or route_kind == "debug":
        return "keep"
    return "sample"
