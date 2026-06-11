from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum


class ErrorKind(StrEnum):
    SYSTEM_FAILURE = "system_failure"
    DOMAIN_REJECTION = "domain_rejection"
    CLIENT_REJECTION = "client_rejection"
    SECURITY_REJECTION = "security_rejection"


class ExceptionCapture(StrEnum):
    NONE = "none"
    STRUCTURED_LOG = "structured_log"
    FULL_EXCEPTION = "full_exception"


class SpanTreatment(StrEnum):
    UNCHANGED = "unchanged"
    RECORD_EVENT = "record_event"
    ERROR = "error"


@dataclass(frozen=True)
class ErrorObservation:
    kind: ErrorKind
    event: str
    severity: str
    exception_capture: ExceptionCapture
    span_treatment: SpanTreatment

    def __post_init__(self) -> None:
        if not self.event:
            raise ValueError("error observation event must not be empty")
        if self.severity not in {"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}:
            raise ValueError("error observation severity must be a standard logging severity")


SYSTEM_FAILURE_OBSERVATION = ErrorObservation(
    kind=ErrorKind.SYSTEM_FAILURE,
    event="exception.recorded",
    severity="ERROR",
    exception_capture=ExceptionCapture.FULL_EXCEPTION,
    span_treatment=SpanTreatment.ERROR,
)

DOMAIN_REJECTION_OBSERVATION = ErrorObservation(
    kind=ErrorKind.DOMAIN_REJECTION,
    event="domain.rejection.recorded",
    severity="INFO",
    exception_capture=ExceptionCapture.STRUCTURED_LOG,
    span_treatment=SpanTreatment.RECORD_EVENT,
)

CLIENT_REJECTION_OBSERVATION = ErrorObservation(
    kind=ErrorKind.CLIENT_REJECTION,
    event="client.rejection.recorded",
    severity="INFO",
    exception_capture=ExceptionCapture.STRUCTURED_LOG,
    span_treatment=SpanTreatment.RECORD_EVENT,
)

SECURITY_REJECTION_OBSERVATION = ErrorObservation(
    kind=ErrorKind.SECURITY_REJECTION,
    event="security.rejection.recorded",
    severity="WARNING",
    exception_capture=ExceptionCapture.STRUCTURED_LOG,
    span_treatment=SpanTreatment.RECORD_EVENT,
)


def observation_for_exception(exc: BaseException) -> ErrorObservation:
    observation = getattr(exc, "observation", None)
    if isinstance(observation, ErrorObservation):
        return observation
    return SYSTEM_FAILURE_OBSERVATION
