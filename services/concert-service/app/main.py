from time import perf_counter

from fastapi import FastAPI, Request, status
from fastapi.responses import JSONResponse, Response
from prometheus_client import Counter, Histogram, generate_latest
from sqlalchemy import text
from sqlalchemy.exc import SQLAlchemyError

from app.config import settings
from app.database import engine
from app.exceptions import register_exception_handlers


HTTP_REQUESTS_TOTAL = Counter(
    "http_requests_total",
    "Total HTTP requests.",
    ["service", "method", "route", "status"],
)
HTTP_REQUEST_DURATION_SECONDS = Histogram(
    "http_request_duration_seconds",
    "HTTP request duration in seconds.",
    ["service", "method", "route", "status"],
)
PROMETHEUS_TEXT_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"


def _route_template(request: Request) -> str:
    route = request.scope.get("route")
    path = getattr(route, "path", None)
    return str(path or request.url.path)


def _readiness_checks() -> dict[str, str]:
    checks: dict[str, str] = {}

    if settings.service_name and settings.database_url:
        checks["config"] = "ok"
    else:
        checks["config"] = "failed: missing required setting"

    try:
        with engine.connect() as connection:
            connection.execute(text("SELECT 1"))
    except SQLAlchemyError as exc:
        checks["database"] = f"failed: {exc.__class__.__name__}"
    else:
        checks["database"] = "ok"

    return checks


def register_metrics_middleware(app: FastAPI) -> None:
    @app.middleware("http")
    async def collect_http_metrics(request: Request, call_next):
        started_at = perf_counter()
        status_code = "500"

        try:
            response = await call_next(request)
            status_code = str(response.status_code)
            return response
        finally:
            route = _route_template(request)
            duration = perf_counter() - started_at
            HTTP_REQUESTS_TOTAL.labels(
                service=settings.service_name,
                method=request.method,
                route=route,
                status=status_code,
            ).inc()
            HTTP_REQUEST_DURATION_SECONDS.labels(
                service=settings.service_name,
                method=request.method,
                route=route,
                status=status_code,
            ).observe(duration)


def create_app() -> FastAPI:
    app = FastAPI(title=settings.service_name)
    register_exception_handlers(app)
    register_metrics_middleware(app)

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    @app.get("/readyz")
    def readyz() -> JSONResponse:
        checks = _readiness_checks()
        is_ready = all(result == "ok" for result in checks.values())
        payload: dict[str, object] = {
            "status": "ready" if is_ready else "not_ready",
            "service": settings.service_name,
            "checks": checks,
        }

        if not is_ready:
            return JSONResponse(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, content=payload)

        return JSONResponse(status_code=status.HTTP_200_OK, content=payload)

    @app.get("/metrics")
    def metrics() -> Response:
        return Response(content=generate_latest(), media_type=PROMETHEUS_TEXT_CONTENT_TYPE)

    return app


app = create_app()
