from __future__ import annotations

import json
from pathlib import Path

import pytest

REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
POSTGRES_TESTS = (
    "services/order-service/tests/integration/test_postgres_order_concurrency.py",
    "services/order-service/tests/integration/test_postgres_payment_failure_idempotency.py",
    "services/payment-service/tests/integration/test_postgres_payment_idempotency.py",
)


def test_postgres_tests_are_discovered_and_require_database() -> None:
    for relative_path in POSTGRES_TESTS:
        path = REPOSITORY_ROOT / relative_path
        assert path.is_file()
        assert path.name.startswith("test_")
        assert not path.with_name(path.name.removeprefix("test_")).exists()
        source = path.read_text(encoding="utf-8")
        assert "os.environ[" in source


def test_lifecycle_tasks_use_unique_compose_projects_and_cleanup() -> None:
    root_taskfile = (REPOSITORY_ROOT / "Taskfile.yml").read_text(encoding="utf-8")
    test_taskfile = (REPOSITORY_ROOT / "tests/Taskfile.yml").read_text(
        encoding="utf-8",
    )
    assert "  test-purchase-postgres-integration:" in root_taskfile
    assert "task: tests:test-purchase-postgres-integration" in root_taskfile
    assert "  purchase-lifecycle-e2e:" in root_taskfile
    assert "task: tests:purchase-lifecycle-e2e" in root_taskfile
    postgres_task = test_taskfile.split(
        "  test-purchase-postgres-integration:",
        1,
    )[1].split("\n  purchase-lifecycle-e2e:", 1)[0]
    assert "PURCHASE_POSTGRES_COMPOSE_PROJECT" in postgres_task
    assert "postgres:16-alpine" in (
        REPOSITORY_ROOT / "tests/e2e/docker-compose.yml"
    ).read_text(encoding="utf-8")
    assert "down -v --remove-orphans" in postgres_task
    assert "ORDER_TEST_DATABASE_URL" in postgres_task
    assert "PAYMENT_TEST_DATABASE_URL" in postgres_task
    assert "NOTIFICATION_TEST_DATABASE_URL" in postgres_task
    lifecycle_task = test_taskfile.split("  purchase-lifecycle-e2e:", 1)[1]
    assert 'COMPOSE_PARALLEL_LIMIT: "1"' in lifecycle_task
    assert "purchase-e2e-with-metrics" in lifecycle_task
    assert "purchase-e2e-with-notification-metrics" in lifecycle_task
    assert "test-purchase-postgres-integration" in lifecycle_task


def test_scenarios_have_no_always_true_progress_assertions() -> None:
    scenarios = REPOSITORY_ROOT / "tests/e2e/scenarios"
    for path in scenarios.glob("*.postman_collection.json"):
        content = path.read_text(encoding="utf-8")
        json.loads(content)
        assert "pm.expect(true)" not in content
    happy_path = (
        scenarios / "04-customer-drop-purchase-happy-path.postman_collection.json"
    ).read_text(encoding="utf-8")
    assert "paymentReadyRetries" in happy_path
    assert "status is 201 before retry timeout" in happy_path
    assert "pm.execution.setNextRequest('Approve mock payment')" in happy_path
    sold_out = (
        scenarios / "06-sold-out-concurrency-flow.postman_collection.json"
    ).read_text(encoding="utf-8")
    assert "body.error.message" in sold_out


def test_catalog_runtime_image_contains_projection_contract_dependencies() -> None:
    dockerfile = (REPOSITORY_ROOT / "services/catalog-service/Dockerfile").read_text(
        encoding="utf-8"
    )
    assert "COPY packages/contracts packages/contracts" in dockerfile
    assert "COPY packages/kafka-utils packages/kafka-utils" in dockerfile


@pytest.mark.parametrize("missing_database", ("ORDER", "PAYMENT", "NOTIFICATION"))
def test_postgres_task_has_no_skip_escape_hatch(missing_database: str) -> None:
    taskfile = (REPOSITORY_ROOT / "tests/Taskfile.yml").read_text(encoding="utf-8")
    task = taskfile.split("  test-purchase-postgres-integration:", 1)[1].split(
        "\n  purchase-lifecycle-e2e:",
        1,
    )[0]
    assert f"{missing_database}_TEST_DATABASE_URL" in task
    assert "--maxfail=1" in task
    assert "--disable-warnings" not in task
