from fastapi import FastAPI
from server.operational import (
    ReadinessCheck,
    register_operational_handlers,
    required_settings_readiness_check,
    sqlalchemy_readiness_check,
)

from app.config import settings
from app.database import engine, init_db
from app.exceptions import register_exception_handlers
from app.observability import configure_app_observability
from app.routers import router as concert_router


def _readiness_checks() -> dict[str, ReadinessCheck]:
    return {
        "config": required_settings_readiness_check(
            {
                "service_name": settings.service_name,
                "database_url": settings.database_url,
            }
        ),
        "database": sqlalchemy_readiness_check(engine),
    }


def create_app() -> FastAPI:
    init_db()
    observability_config = settings.observability_config()
    app = FastAPI(title=settings.service_name)
    configure_app_observability(app, observability_config)
    register_exception_handlers(app)
    register_operational_handlers(
        app,
        service_name=settings.service_name,
        service_version=observability_config.service_version,
        service_environment=observability_config.service_environment,
        readiness_checks=_readiness_checks(),
    )

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    app.include_router(concert_router)

    return app


app = create_app()
