from observability.config import (
    DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS,
    OBSERVABILITY_ENV_KEYS,
    ObservabilityConfig,
    observability_config_from_env,
)
from observability.database import instrument_motor_client, instrument_sqlalchemy_engine
from observability.exceptions import ErrorRecordingMiddleware, record_exception
from observability.fastapi import (
    REQUEST_ID_HEADER,
    RequestIdMiddleware,
    create_request_log_middleware,
    get_current_request_id,
    instrument_fastapi_app,
    request_id_middleware_options,
)
from observability.fastapi_errors import HttpError, error_response, register_error_handlers
from observability.logging import configure_process_logging, configure_structured_logging
from observability.observation import (
    CLIENT_REJECTION_OBSERVATION,
    DOMAIN_REJECTION_OBSERVATION,
    SECURITY_REJECTION_OBSERVATION,
    SYSTEM_FAILURE_OBSERVATION,
    ErrorKind,
    ErrorObservation,
    ExceptionCapture,
    SpanTreatment,
)
from observability.tracing import (
    NoopTraceRecorder,
    TraceRecorder,
    configure_process_tracing,
    configure_tracing,
    set_current_span_attributes,
    trace_recorder,
)

__all__ = [
    "OBSERVABILITY_ENV_KEYS",
    "DEFAULT_FASTAPI_TRACE_EXCLUDED_URLS",
    "REQUEST_ID_HEADER",
    "RequestIdMiddleware",
    "ErrorRecordingMiddleware",
    "HttpError",
    "ObservabilityConfig",
    "NoopTraceRecorder",
    "CLIENT_REJECTION_OBSERVATION",
    "DOMAIN_REJECTION_OBSERVATION",
    "SECURITY_REJECTION_OBSERVATION",
    "SYSTEM_FAILURE_OBSERVATION",
    "ErrorKind",
    "ErrorObservation",
    "configure_process_logging",
    "configure_process_tracing",
    "configure_structured_logging",
    "configure_tracing",
    "create_request_log_middleware",
    "error_response",
    "ExceptionCapture",
    "get_current_request_id",
    "instrument_motor_client",
    "instrument_fastapi_app",
    "instrument_sqlalchemy_engine",
    "observability_config_from_env",
    "record_exception",
    "register_error_handlers",
    "request_id_middleware_options",
    "set_current_span_attributes",
    "SpanTreatment",
    "TraceRecorder",
    "trace_recorder",
]
