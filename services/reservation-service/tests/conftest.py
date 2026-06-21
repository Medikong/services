import os

import pytest


os.environ.setdefault("SERVICE_VERSION", "test-version")
os.environ.setdefault("SERVICE_ENVIRONMENT", "test")


@pytest.fixture(autouse=True)
def reset_app_sqlite_db():
    from app import entities  # noqa: F401
    from app.database import Base, engine

    if not engine.url.drivername.startswith("sqlite"):
        yield
        return

    Base.metadata.drop_all(engine)
    Base.metadata.create_all(engine)
    try:
        yield
    finally:
        Base.metadata.drop_all(engine)


def pytest_addoption(parser):
    parser.addoption(
        "--run-reservation-api-benchmark",
        action="store_true",
        default=False,
        help="Run the isolated PostgreSQL API benchmark for reservation-service.",
    )
    parser.addoption("--reservation-benchmark-samples", type=int, default=30)
    parser.addoption("--reservation-benchmark-warmup", type=int, default=3)
    parser.addoption(
        "--reservation-benchmark-artifact-dir",
        default="tests/tmp/reports/reservation-api-benchmark",
    )
    parser.addoption("--reservation-benchmark-preset", default="smoke")
