from fastapi import FastAPI
from server.operational import (
    ReadinessCheck,
    register_operational_handlers,
    required_settings_readiness_check,
    sqlalchemy_readiness_check,
)

from app.config import settings
from app.database import engine
from app.exceptions import register_exception_handlers


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
    app = FastAPI(title=settings.service_name)
    register_exception_handlers(app)
    register_operational_handlers(
        app,
        service_name=settings.service_name,
        readiness_checks=_readiness_checks(),
    )

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    return app


app = create_app()
