import logging
import os
from contextvars import ContextVar
from time import perf_counter
from uuid import uuid4

import structlog
from asgi_correlation_id import CorrelationIdMiddleware, correlation_id
from fastapi import FastAPI, Request
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.logging import LoggingInstrumentor
from opentelemetry.sdk.resources import DEPLOYMENT_ENVIRONMENT, SERVICE_NAME, SERVICE_VERSION, Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor


REQUEST_ID_HEADER = "X-Request-Id"
request_id_context: ContextVar[str | None] = ContextVar("request_id", default=None)
_logging_configured = False
_logging_instrumented = False
_tracing_configured = False


def get_current_request_id() -> str | None:
    return request_id_context.get()


def setup_request_observability(
    app: FastAPI,
    service_name: str,
    *,
    service_version: str | None = None,
    service_environment: str | None = None,
) -> None:
    configure_structured_logging()
    configure_tracing(
        service_name=service_name,
        service_version=service_version or os.getenv("SERVICE_VERSION"),
        service_environment=service_environment or os.getenv("SERVICE_ENVIRONMENT"),
    )
    FastAPIInstrumentor.instrument_app(app)
    app.add_middleware(
        CorrelationIdMiddleware,
        header_name=REQUEST_ID_HEADER,
        update_request_header=True,
        validator=_valid_request_id,
    )

    logger = structlog.get_logger(service_name)

    @app.middleware("http")
    async def request_observability_middleware(request: Request, call_next):
        request_id = _request_id(request)
        request.state.request_id = request_id
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
            trace_id, span_id = _current_trace_context()
            logger.info(
                "http.request.completed",
                **{
                    "service.name": service_name,
                    "severity": "INFO",
                    "severity_text": "INFO",
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "request_id": request_id,
                    "http.method": request.method,
                    "http.route": route,
                    "http.status_code": status_code,
                    "duration_ms": int(duration_seconds * 1000),
                },
            )
            request_id_context.reset(request_id_token)


def configure_structured_logging() -> None:
    global _logging_configured, _logging_instrumented

    if not _logging_instrumented:
        LoggingInstrumentor().instrument(set_logging_format=False)
        _logging_instrumented = True

    if _logging_configured:
        return

    logging.basicConfig(level=logging.INFO, format="%(message)s")
    structlog.configure(
        processors=[
            structlog.contextvars.merge_contextvars,
            structlog.processors.TimeStamper(key="timestamp", fmt="iso", utc=True),
            structlog.processors.JSONRenderer(separators=(",", ":")),
        ],
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.stdlib.BoundLogger,
        cache_logger_on_first_use=True,
    )
    _logging_configured = True


def configure_tracing(
    *,
    service_name: str,
    service_version: str | None = None,
    service_environment: str | None = None,
) -> None:
    global _tracing_configured

    if _tracing_configured or os.getenv("OTEL_SDK_DISABLED", "").lower() == "true":
        return

    attributes: dict[str, str] = {SERVICE_NAME: service_name}
    if service_version:
        attributes[SERVICE_VERSION] = service_version
    if service_environment:
        attributes[DEPLOYMENT_ENVIRONMENT] = service_environment

    provider = TracerProvider(resource=Resource.create(attributes))
    if _otlp_trace_export_enabled():
        provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)
    _tracing_configured = True


def _otlp_trace_export_enabled() -> bool:
    traces_exporter = os.getenv("OTEL_TRACES_EXPORTER", "otlp").lower()
    if traces_exporter == "none":
        return False
    return bool(os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT") or os.getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))


def _request_id(request: Request) -> str:
    return correlation_id.get() or request.headers.get(REQUEST_ID_HEADER) or request.headers.get("x-request-id") or str(uuid4())


def _valid_request_id(request_id: str) -> bool:
    return bool(request_id.strip())


def _current_trace_context() -> tuple[str, str]:
    span_context = trace.get_current_span().get_span_context()
    if not span_context.is_valid:
        return "", ""
    return format(span_context.trace_id, "032x"), format(span_context.span_id, "016x")


def _route_template(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return str(path or request.url.path)
