from __future__ import annotations

import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS_DIR))

import run_purchase_internal_regression as runner


EXPECTED_TASKS = (
    "test-services",
    "test-purchase-contracts",
    "test-purchase-postgres-integration",
    "purchase-e2e-with-metrics",
    "purchase-e2e-concurrency",
    "payment-failure-idempotency",
    "purchase-lifecycle-e2e",
    "purchase-e2e-with-traces",
    "purchase-e2e-with-kafka-traces",
    "purchase-e2e-with-log-correlation",
    "purchase-e2e-with-notification-metrics",
)


def make_config(tmp_path: Path) -> runner.RunnerConfig:
    source_root = tmp_path / "source"
    source_root.mkdir()
    (source_root / ".git").mkdir()
    executables = tuple(tmp_path / name for name in ("task.exe", "git.exe", "docker.exe"))
    for executable in executables:
        executable.touch()
    return runner.RunnerConfig(
        source_root=source_root,
        git_dir=source_root / ".git",
        task_bin=executables[0],
        git_bin=executables[1],
        docker_bin=executables[2],
        project_prefix="dropmong-g009-internal-regression",
        start_gate=1,
    )


def completed(argv: tuple[str, ...], stdout: str = "") -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(argv, 0, stdout, "")


def test_full_lifecycle_gates_have_dependency_order_and_unique_resources() -> None:
    run_prefix = "dropmong-g009-internal-regression-a1b2c3d4"
    gates = runner.build_gates(run_prefix)
    assert tuple(gate.task_name for gate in gates) == EXPECTED_TASKS
    assert len({project for gate in gates for project in gate.cleanup_projects}) == 11
    assert all(
        dict(gate.variables)["TEST_RUNNER_IMAGE_PREFIX"].startswith(run_prefix)
        for gate in gates
    )
    lifecycle = gates[6]
    assert dict(lifecycle.variables)["PURCHASE_LIFECYCLE_COMPOSE_PREFIX"] == (
        f"{run_prefix}-lifecycle"
    )
    assert lifecycle.cleanup_projects == (
        f"{run_prefix}-lifecycle-postgres",
        f"{run_prefix}-lifecycle-metrics",
        f"{run_prefix}-lifecycle-notifications-notification-metrics",
    )


def test_gate_duration_is_emitted_and_failure_stops_following_gate(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    config = make_config(tmp_path)
    gates = runner.build_gates("dropmong-g009-internal-regression-a1b2c3d4")[:3]
    calls: list[tuple[str, ...]] = []
    clock = iter((10.0, 12.25, 20.0, 23.5))

    def fake_process(
        argv: tuple[str, ...],
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        del cwd, env
        calls.append(argv)
        return subprocess.CompletedProcess(argv, 17 if len(calls) == 2 else 0, "", "")

    monkeypatch.setattr(runner, "run_process", fake_process)
    monkeypatch.setattr(runner.time, "monotonic", lambda: next(clock))
    with pytest.raises(runner.RunnerExecutionError) as error:
        runner.run_gates(config, tmp_path / "clone", gates)
    assert error.value.exit_code == 17
    assert len(calls) == 2
    output = capsys.readouterr().out
    assert "duration_seconds=2.250" in output
    assert "duration_seconds=3.500" in output


def test_cleanup_removes_owned_resources_and_reports_all_zero(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    config = make_config(tmp_path)
    gates = runner.build_gates("dropmong-g009-internal-regression-a1b2c3d4")[:1]
    queries: dict[tuple[str, ...], int] = {}
    image_removals: list[tuple[str, ...]] = []

    def fake_process(
        argv: tuple[str, ...],
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        del cwd, env
        queries[argv] = queries.get(argv, 0) + 1
        if argv[1:4] == ("image", "rm", "--force"):
            image_removals.append(argv)
        if "ls" in argv and queries[argv] == 1:
            if argv[1] == "image":
                return completed(argv, "owned-runner-order-service:local\n")
            return completed(argv, "resource-id\n")
        return completed(argv)

    monkeypatch.setattr(runner, "run_process", fake_process)
    runner.cleanup_resources(
        config,
        gates,
        runner.CleanupIO(fake_process, runner._emit_output),
    )
    output = capsys.readouterr().out
    assert "containers=0" in output
    assert "networks=0" in output
    assert "volumes=0" in output
    assert "images=0" in output
    assert any(argv[1:4] == ("image", "ls", "--format") for argv in queries)
    assert image_removals
    assert image_removals[0][-1] == "owned-runner-order-service:local"
