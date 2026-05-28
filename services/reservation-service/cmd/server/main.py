import uvicorn

from app.config import settings
from app.main import create_app


def main() -> None:
    uvicorn.run(create_app(), host="0.0.0.0", port=settings.port)


if __name__ == "__main__":
    main()
