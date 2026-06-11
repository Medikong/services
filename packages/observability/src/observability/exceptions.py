from __future__ import annotations

import asyncio
import traceback
from collections.abc import Mapping
from concurrent.futures import CancelledError as FutureCancelledError
from typing import Final

import structlog
from middleware.request_context import get_current_request_id
from middleware.types import ASGIApp, Receive, Scope, Send
from opentelemetry import trace
from opentelemetry.trace import Status, StatusCode
from opentelemetry.util.types import AttributeValue

from observability.error_context import extract_error_context
from observability.observation import ExceptionCapture, SpanTreatment, observation_for_exception
from observability.tracing import current_trace_context, set_current_span_attributes


_RECORDED_ATTR: Final = "__medikong_observability_recorded__"


def record_exception(
    exc: BaseException,
    *,
    service_name: str,
    attributes: Mapping[str, AttributeValue] | None = None,
) -> bool:
    """Record one exception on the current span and structured log."""
    if is_exception_recorded(exc):
        return False

    mark_exception_recorded(exc)
    observation = observation_for_exception(exc)
    error_attributes: dict[str, AttributeValue] = {
        "error.type": type(exc).__name__,
        "error.kind": observation.kind.value,
        **extract_error_context(exc),
    }
    if attributes:
        error_attributes.update(attributes)

    span = trace.get_current_span()
    if span.get_span_context().is_valid:
        if observation.exception_capture is ExceptionCapture.FULL_EXCEPTION:
            span.record_exception(exc)
        if observation.span_treatment is SpanTreatment.RECORD_EVENT:
            span.add_event(observation.event, attributes=error_attributes)
        elif observation.span_treatment is SpanTreatment.ERROR:
            span.set_status(Status(StatusCode.ERROR, str(exc)))
        set_current_span_attributes(error_attributes)

    if observation.exception_capture is not ExceptionCapture.NONE:
        trace_id, span_id = current_trace_context()
        log_fields: dict[str, object] = {
            "service.name": service_name,
            "severity": observation.severity,
            "severity_text": observation.severity,
            "trace_id": trace_id,
            "span_id": span_id,
            "request_id": get_current_request_id(),
            "error.type": type(exc).__name__,
            "error.message": str(exc),
            **error_attributes,
        }
        if observation.exception_capture is ExceptionCapture.FULL_EXCEPTION:
            log_fields["exception.stacktrace"] = "".join(
                traceback.format_exception(type(exc), exc, exc.__traceback__)
            )
        _log_observation(service_name, observation.severity, observation.event, log_fields)
    return True


def is_exception_recorded(exc: BaseException) -> bool:
    return bool(getattr(exc, _RECORDED_ATTR, False))


def mark_exception_recorded(exc: BaseException) -> None:
    setattr(exc, _RECORDED_ATTR, True)


def _log_observation(service_name: str, severity: str, event: str, fields: dict[str, object]) -> None:
    logger = structlog.get_logger(service_name)
    match severity:
        case "DEBUG":
            logger.debug(event, **fields)
        case "INFO":
            logger.info(event, **fields)
        case "WARNING":
            logger.warning(event, **fields)
        case "ERROR":
            logger.error(event, **fields)
        case "CRITICAL":
            logger.critical(event, **fields)
        case _:
            raise ValueError(f"unsupported error observation severity: {severity}")


class ErrorRecordingMiddleware:
    """Record unhandled exceptions and let downstream recovery decide the response."""

    def __init__(self, app: ASGIApp, *, service_name: str) -> None:
        self.app = app
        self.service_name = service_name

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        try:
            await self.app(scope, receive, send)
        except Exception as exc:
            if _is_cancellation(exc):
                raise
            record_exception(exc, service_name=self.service_name)
            raise


def _is_cancellation(exc: Exception) -> bool:
    return isinstance(exc, asyncio.CancelledError | FutureCancelledError)
