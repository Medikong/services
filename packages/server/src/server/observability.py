import logging
from collections.abc import Mapping
from contextvars import ContextVar
from dataclasses import dataclass
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
OBSERVABILITY_ENV_KEYS = (
    "SERVICE_VERSION",
    "SERVICE_ENVIRONMENT",
    "OTEL_SDK_DISABLED",
    "OTEL_TRACES_EXPORTER",
    "OTEL_EXPORTER_OTLP_ENDPOINT",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
)


@dataclass(frozen=True)
class ObservabilityConfig:
    service_name: str
    service_version: str | None = None
    service_environment: str | None = None
    otel_sdk_disabled: bool = False
    otel_traces_exporter: str = "otlp"
    otlp_trace_exporter_endpoint: str | None = None


def get_current_request_id() -> str | None:
    return request_id_context.get()


def observability_config_from_env(
    service_name: str,
    *,
    env: Mapping[str, str],
) -> ObservabilityConfig:
    otlp_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_ENDPOINT")
    otlp_traces_endpoint = _optional_env(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
    return ObservabilityConfig(
        service_name=service_name,
        service_version=_optional_env(env, "SERVICE_VERSION"),
        service_environment=_optional_env(env, "SERVICE_ENVIRONMENT"),
        otel_sdk_disabled=env.get("OTEL_SDK_DISABLED", "").lower() == "true",
        otel_traces_exporter=env.get("OTEL_TRACES_EXPORTER", "otlp"),
        otlp_trace_exporter_endpoint=otlp_traces_endpoint or otlp_endpoint,
    )


def setup_request_observability(app: FastAPI, config: ObservabilityConfig) -> None:
    configure_structured_logging()
    configure_tracing(config)
    FastAPIInstrumentor.instrument_app(app)
    app.add_middleware(
        CorrelationIdMiddleware,
        header_name=REQUEST_ID_HEADER,
        update_request_header=True,
        validator=_valid_request_id,
    )

    logger = structlog.get_logger(config.service_name)

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
                    "service.name": config.service_name,
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


def configure_tracing(config: ObservabilityConfig) -> None:
    global _tracing_configured

    if _tracing_configured or config.otel_sdk_disabled:
        return

    attributes: dict[str, str] = {SERVICE_NAME: config.service_name}
    if config.service_version:
        attributes[SERVICE_VERSION] = config.service_version
    if config.service_environment:
        attributes[DEPLOYMENT_ENVIRONMENT] = config.service_environment

    provider = TracerProvider(resource=Resource.create(attributes))
    if _otlp_trace_export_enabled(config):
        provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=config.otlp_trace_exporter_endpoint)))
    trace.set_tracer_provider(provider)
    _tracing_configured = True


def _otlp_trace_export_enabled(config: ObservabilityConfig) -> bool:
    traces_exporter = config.otel_traces_exporter.strip().lower()
    if traces_exporter == "none":
        return False
    if traces_exporter != "otlp":
        return False
    return bool(config.otlp_trace_exporter_endpoint)


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


def _optional_env(env: Mapping[str, str], name: str) -> str | None:
    value = env.get(name)
    if value is None or value == "":
        return None
    return value
