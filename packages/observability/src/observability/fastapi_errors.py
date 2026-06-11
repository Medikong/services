from __future__ import annotations

from collections.abc import Callable, Mapping
from datetime import UTC, datetime
from uuid import uuid4

from errors import ContextualError, in_domain
from fastapi import FastAPI, Request, status
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException

from observability.exceptions import record_exception
from observability.observation import ErrorObservation, SYSTEM_FAILURE_OBSERVATION


StatusCodeMapper = Callable[[int], str]


class HttpError(ContextualError):
    observation: ErrorObservation = SYSTEM_FAILURE_OBSERVATION

    def __init__(
        self,
        status_code: int,
        code: str,
        message: str,
        details: Mapping[str, object] | None = None,
        *,
        domain: str | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.message = message
        self.details = dict(details) if details is not None else None

        builder = in_domain(domain or _domain_from_code(code)).code(code).public(message)
        for key, value in (self.details or {}).items():
            if isinstance(value, str | int | float | bool):
                builder = builder.with_attr(key, value)
        builder.attach(self)


def register_error_handlers(
    app: FastAPI,
    *,
    service_name: str,
    domain: str,
    http_error_code_for_status: StatusCodeMapper | None = None,
) -> None:
    @app.exception_handler(HttpError)
    async def handle_http_error(request: Request, exc: HttpError) -> JSONResponse:
        record_exception(exc, service_name=service_name, attributes={"http.status_code": exc.status_code})
        return error_response(request, exc.status_code, exc.code, exc.message, exc.details)

    @app.exception_handler(HTTPException)
    async def handle_starlette_http_error(request: Request, exc: HTTPException) -> JSONResponse:
        code = _mapped_status_code(exc.status_code, domain, http_error_code_for_status)
        message = str(exc.detail)
        contextual = HttpError(exc.status_code, code, message, domain=domain)
        contextual.__traceback__ = exc.__traceback__
        record_exception(contextual, service_name=service_name, attributes={"http.status_code": exc.status_code})
        return error_response(request, exc.status_code, code, message)

    @app.exception_handler(RequestValidationError)
    async def handle_validation_error(request: Request, exc: RequestValidationError) -> JSONResponse:
        details = {"errors": exc.errors()}
        contextual = HttpError(
            status.HTTP_422_UNPROCESSABLE_ENTITY,
            "request.validation_failed",
            "Request validation failed.",
            details,
            domain=domain,
        )
        contextual.__traceback__ = exc.__traceback__
        record_exception(
            contextual,
            service_name=service_name,
            attributes={"http.status_code": status.HTTP_422_UNPROCESSABLE_ENTITY},
        )
        return error_response(
            request,
            status.HTTP_422_UNPROCESSABLE_ENTITY,
            "request.validation_failed",
            "Request validation failed.",
            details,
        )


def error_response(
    request: Request,
    status_code: int,
    code: str,
    message: str,
    details: object | None = None,
) -> JSONResponse:
    request_id = getattr(request.state, "request_id", None) or request.headers.get("X-Request-Id") or f"req-{uuid4()}"
    error: dict[str, object] = {"code": code, "message": message}
    if details is not None:
        error["details"] = details
    return JSONResponse(
        status_code=status_code,
        content={
            "error": error,
            "requestId": request_id,
            "occurredAt": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        },
    )


def _mapped_status_code(status_code: int, domain: str, mapper: StatusCodeMapper | None) -> str:
    if mapper is not None:
        return mapper(status_code)
    if status_code == status.HTTP_401_UNAUTHORIZED:
        return f"{domain}.unauthorized"
    if status_code == status.HTTP_403_FORBIDDEN:
        return f"{domain}.forbidden"
    if status_code == status.HTTP_404_NOT_FOUND:
        return f"{domain}.not_found"
    if status_code == status.HTTP_503_SERVICE_UNAVAILABLE:
        return "service.unavailable"
    return "request.failed"


def _domain_from_code(code: str) -> str:
    domain, _, _ = code.partition(".")
    return domain or "request"
