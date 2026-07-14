from __future__ import annotations

import json
import logging
import subprocess
import sys
from pathlib import Path

import pytest


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import run_payment_failure_idempotency as runner  # noqa: E402
from run_payment_failure_idempotency import (  # noqa: E402
    CleanupFailure,
    ComposeConfig,
    RunnerConfig,
    RunnerInputError,
    execute,
    run_scenario,
)


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def test_payment_failure_task_uses_python_runner() -> None:
    taskfile = (SERVICE_ROOT / "tests" / "Taskfile.yml").read_text(encoding="utf-8")
    payment_task = taskfile.split("  payment-failure-idempotency:", 1)[1].split(
        "\n  purchase-e2e-up:",
        1,
    )[0]

    assert "run_payment_failure_idempotency.py" in payment_task
    assert "bash " not in payment_task


def test_run_scenario_uses_compose_smoke_service_and_exact_database_assertions(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    run_id = "payment-failure-idempotency-20260713T010203Z-42"
    config = RunnerConfig(
        source_root=tmp_path,
        compose_relative=Path("tests/e2e/docker-compose.yml"),
        compose=ComposeConfig(
            command=("docker", "compose"),
            project="dropmong-payment-green",
            compose_file=tmp_path / "tests/e2e/docker-compose.yml",
        ),
        wait_timeout_seconds=180,
        scenario_timeout_seconds=60,
        smoke_image=None,
        run_id=run_id,
    )
    observed: list[tuple[str, ...]] = []

    def fake_run_process(
        argv: tuple[str, ...],
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        del cwd, env
        observed.append(argv)
        stdout = ""
        if argv[-1] == "payment-failure-idempotency-smoke" and "run" in argv:
            stdout = json.dumps(
                {
                    "ok": True,
                    "order_id": "order-1",
                    "payment_id": "payment-1",
                    "run_id": run_id,
                    "unique_event_ids": ["event-1"],
                    "user_id": f"{run_id}-user",
                },
            )
        elif "psql" in argv:
            query = argv[-1]
            if "FROM payments" in query:
                stdout = "1|1|payment-1|payment-1\n"
            elif "FROM processed_events" in query:
                stdout = "1|1|payment.failed|order|order-1\n"
            else:
                stdout = "1|PAYMENT_FAILED|payment-1\n"
        return subprocess.CompletedProcess(argv, 0, stdout, "")

    monkeypatch.setattr(runner, "run_process", fake_run_process)

    run_scenario(config)

    up = next(command for command in observed if "up" in command)
    assert up[-5:] == (
        "postgres",
        "kafka",
        "kafka-init",
        "order-service",
        "payment-service",
    )
    smoke = next(
        command
        for command in observed
        if "run" in command and command[-1] == "payment-failure-idempotency-smoke"
    )
    assert smoke[-5:] == (
        "run",
        "--rm",
        "--no-deps",
        "-T",
        "payment-failure-idempotency-smoke",
    )
    assert "-v" not in smoke
    assert sum("psql" in command for command in observed) == 3


def test_execute_preserves_primary_failure_when_cleanup_also_fails(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    caplog: pytest.LogCaptureFixture,
) -> None:
    context = tmp_path / "context"
    context.mkdir()
    config = RunnerConfig(
        source_root=tmp_path,
        compose_relative=Path("tests/e2e/docker-compose.yml"),
        compose=ComposeConfig(
            command=("docker", "compose"),
            project="dropmong-payment-green",
            compose_file=tmp_path / "tests/e2e/docker-compose.yml",
        ),
        wait_timeout_seconds=180,
        scenario_timeout_seconds=60,
        smoke_image=None,
        run_id="payment-failure-idempotency-20260713T010203Z-42",
    )
    primary = RunnerInputError("primary failure")

    def fake_mkdtemp(*, prefix: str) -> str:
        del prefix
        return str(context)

    def fail_context(source_root: Path, destination: Path) -> None:
        del source_root, destination
        raise primary

    def fail_cleanup(
        compose: ComposeConfig,
        temporary_context: Path,
        environment: dict[str, str],
    ) -> CleanupFailure:
        del compose, temporary_context, environment
        return CleanupFailure(("compose down failed", "context removal failed"))

    monkeypatch.setattr(runner.tempfile, "mkdtemp", fake_mkdtemp)
    monkeypatch.setattr(runner, "create_tracked_context", fail_context)
    monkeypatch.setattr(runner, "cleanup", fail_cleanup)

    with caplog.at_level(logging.ERROR), pytest.raises(RunnerInputError) as captured:
        execute(config)

    assert captured.value is primary
    assert "cleanup failed after original failure" in caplog.text
