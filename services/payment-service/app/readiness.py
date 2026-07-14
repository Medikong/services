from datetime import UTC, datetime

from fastapi import Response, status
from sqlalchemy.exc import SQLAlchemyError

from app.db import AppResources, database_schema_is_current
from app.models import ReadinessResponse


async def payment_readiness(
    resources: AppResources,
    response: Response,
    service_name: str,
) -> ReadinessResponse:
    checks = {"payments": "ok", "order_created_handler": "ok"}
    if resources.engine is not None:
        try:
            database_ready = await database_schema_is_current(resources.engine)
        except (ConnectionError, TimeoutError, SQLAlchemyError):
            checks["database_schema"] = "unreachable"
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
            return ReadinessResponse(
                status="not_ready",
                service=service_name,
                checks=checks,
                timestamp=datetime.now(UTC),
            )
        checks["database_schema"] = "ok" if database_ready else "migration_required"
        if not database_ready:
            response.status_code = status.HTTP_503_SERVICE_UNAVAILABLE
            return ReadinessResponse(
                status="not_ready",
                service=service_name,
                checks=checks,
                timestamp=datetime.now(UTC),
            )
    return ReadinessResponse(
        status="ready",
        service=service_name,
        checks=checks,
        timestamp=datetime.now(UTC),
    )
