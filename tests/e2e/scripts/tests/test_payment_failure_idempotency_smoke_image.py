from __future__ import annotations

from pathlib import Path


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def test_compose_smoke_service_builds_copied_scripts_without_bind_mount() -> None:
    compose = (SERVICE_ROOT / "tests/e2e/docker-compose.yml").read_text(
        encoding="utf-8",
    )
    service = compose.split("  payment-failure-idempotency-smoke:", 1)[1].split(
        "\nvolumes:",
        1,
    )[0]
    dockerfile = (
        SERVICE_ROOT / "tests/e2e/payment-failure-idempotency-smoke/Dockerfile"
    ).read_text(encoding="utf-8")

    assert "dockerfile: tests/e2e/payment-failure-idempotency-smoke/Dockerfile" in service
    assert "volumes:" not in service
    assert "PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE" in service
    assert "COPY tests/e2e/scripts/payment-failure-idempotency-smoke.py" in dockerfile
    assert "COPY tests/e2e/scripts/payment_failure_idempotency_support.py" in dockerfile
    assert "COPY packages/contracts/src/contracts" in dockerfile
    assert all(name in dockerfile for name in ("aiokafka", "anyio", "pydantic"))
