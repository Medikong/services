import uvicorn

from app.config import settings


def main() -> None:
    uvicorn.run(
        "app.main:create_app",
        factory=True,
        host="0.0.0.0",
        port=settings.port,
        access_log=False,
        workers=settings.uvicorn_workers,
    )


if __name__ == "__main__":
    main()
