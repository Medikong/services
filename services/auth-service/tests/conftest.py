import os
from pathlib import Path


os.environ.setdefault("SERVICE_VERSION", "test-version")
os.environ.setdefault("SERVICE_ENVIRONMENT", "test")
Path("test_auth_service.db").unlink(missing_ok=True)
os.environ["DATABASE_URL"] = "sqlite:///./test_auth_service.db"


def pytest_addoption(parser):
    parser.addoption(
        "--run-auth-api-benchmark",
        action="store_true",
        default=False,
        help="Run the isolated PostgreSQL API benchmark for auth-service.",
    )
    parser.addoption("--auth-benchmark-samples", type=int, default=30)
    parser.addoption("--auth-benchmark-warmup", type=int, default=3)
    parser.addoption(
        "--auth-benchmark-artifact-dir",
        default="tests/tmp/reports/auth-api-benchmark",
    )
    parser.addoption("--auth-benchmark-preset", default="smoke")
