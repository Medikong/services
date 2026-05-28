from fastapi import FastAPI

from app.config import settings
from app.exceptions import register_exception_handlers


def create_app() -> FastAPI:
    app = FastAPI(title=settings.service_name)
    register_exception_handlers(app)

    @app.get("/health")
    def health() -> dict[str, str]:
        return {"status": "ok", "service": settings.service_name}

    return app


app = create_app()
