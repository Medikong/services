from collections.abc import Awaitable, Callable
from contextvars import ContextVar
from time import perf_counter
from uuid import uuid4

import structlog
from asgi_correlation_id import CorrelationIdMiddleware, correlation_id
from fastapi import FastAPI, Request
from starlette.responses import Response

from middleware import get_current_client_action_id
from middleware import get_current_request_id as get_runtime_request_id
from observability.config import ObservabilityConfig
from observability.tracing import current_trace_context


REQUEST_ID_HEADER = "X-Request-Id"
RequestIdMiddleware = CorrelationIdMiddleware
RequestHandler = Callable[[Request], Awaitable[Response]]
RequestMiddleware = Callable[[Request, RequestHandler], Awaitable[Response]]
request_id_context: ContextVar[str | None] = ContextVar("request_id", default=None)


def get_current_request_id() -> str | None:
    return request_id_context.get()


def instrument_fastapi_app(app: FastAPI) -> None:
    # Inbound request spans are automatic; manual spans stay out of service layers until a use case proves it.
    from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor

    FastAPIInstrumentor.instrument_app(app)


def request_id_middleware_options() -> dict[str, object]:
    # Request IDs are kept separate from trace IDs for support tickets, access logs, and client-visible lookup.
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
            response = await call_next(request)
            status_code = response.status_code
            response.headers[REQUEST_ID_HEADER] = request_id
            return response
        finally:
            route = _route_template(request)
            duration_seconds = perf_counter() - started_at
            # Logs go through stdout while traces go through OTLP, so IDs are the join keys across Loki and Tempo.
            trace_id, span_id = current_trace_context()
            logger.info(
                "http.request.completed",
                **{
                    "service.name": config.service_name,
                    "severity": "INFO",
                    "severity_text": "INFO",
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "request_id": request_id,
                    "client_action_id": client_action_id,
                    "http.method": request.method,
                    "http.route": route,
                    "http.status_code": status_code,
                    "duration_ms": int(duration_seconds * 1000),
                },
            )
            request_id_context.reset(request_id_token)

    return request_observability_middleware


def _request_id(request: Request) -> str:
    return (
        get_runtime_request_id()
        or correlation_id.get()
        or request.headers.get(REQUEST_ID_HEADER)
        or request.headers.get("x-request-id")
        or str(uuid4())
    )


def _valid_request_id(request_id: str) -> bool:
    return bool(request_id.strip())


def _route_template(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return str(path or request.url.path)
