import os


os.environ.setdefault("SERVICE_VERSION", "test-version")
os.environ.setdefault("SERVICE_ENVIRONMENT", "test")


def pytest_addoption(parser):
    parser.addoption(
        "--run-concert-api-benchmark",
        action="store_true",
        default=False,
        help="Run the isolated PostgreSQL API benchmark for concert-service.",
    )
    parser.addoption("--concert-benchmark-samples", type=int, default=30)
    parser.addoption("--concert-benchmark-warmup", type=int, default=3)
    parser.addoption(
        "--concert-benchmark-artifact-dir",
        default="tests/tmp/reports/concert-api-benchmark",
    )
    parser.addoption("--concert-benchmark-preset", default="smoke")
    parser.addoption(
        "--run-public-api-benchmark",
        action="store_true",
        default=False,
        help="Run the isolated PostgreSQL public API benchmark for concert-service.",
    )
    parser.addoption("--public-benchmark-concerts", type=int, default=1000)
    parser.addoption("--public-benchmark-showtimes-per-concert", type=int, default=4)
    parser.addoption("--public-benchmark-seats-per-showtime", type=int, default=100)
    parser.addoption("--public-benchmark-samples", type=int, default=50)
    parser.addoption("--public-benchmark-warmup", type=int, default=5)
    parser.addoption(
        "--public-benchmark-artifact-dir",
        default="tests/tmp/reports/concert-public-api-benchmark",
    )
