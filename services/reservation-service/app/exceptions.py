from datetime import UTC, datetime
from uuid import uuid4

from fastapi import FastAPI, Request, status
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse


class AppError(Exception):
    def __init__(self, status_code: int, code: str, message: str, details: dict[str, object] | None = None) -> None:
        self.status_code = status_code
        self.code = code
        self.message = message
        self.details = details


class NotFoundError(AppError):
    def __init__(self, resource: str, resource_id: str) -> None:
        super().__init__(
            status.HTTP_404_NOT_FOUND,
            f"{resource}.not_found",
            f"{resource} not found.",
            {"id": resource_id},
        )


class ConflictError(AppError):
    def __init__(self, code: str, message: str, details: dict[str, object] | None = None) -> None:
        super().__init__(status.HTTP_409_CONFLICT, code, message, details)


def error_response(request: Request, status_code: int, code: str, message: str, details: object | None = None) -> JSONResponse:
    request_id = request.headers.get("X-Request-Id") or f"req-{uuid4()}"
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


def register_exception_handlers(app: FastAPI) -> None:
    @app.exception_handler(AppError)
    async def handle_app_error(request: Request, exc: AppError) -> JSONResponse:
        return error_response(request, exc.status_code, exc.code, exc.message, exc.details)

    @app.exception_handler(RequestValidationError)
    async def handle_validation_error(request: Request, exc: RequestValidationError) -> JSONResponse:
        return error_response(
            request,
            status.HTTP_422_UNPROCESSABLE_ENTITY,
            "request.validation_failed",
            "Request validation failed.",
            {"errors": exc.errors()},
        )
