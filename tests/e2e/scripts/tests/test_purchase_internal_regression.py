from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parents[1]
REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(SCRIPTS_DIR))

import run_purchase_internal_regression as runner


EXPECTED_TASKS = (
    "test-services",
    "purchase-e2e-with-metrics",
    "purchase-e2e-concurrency",
    "payment-failure-idempotency",
    "purchase-e2e-with-traces",
    "purchase-e2e-with-kafka-traces",
    "purchase-e2e-with-log-correlation",
    "purchase-e2e-with-notification-metrics",
)


def make_config(tmp_path: Path) -> runner.RunnerConfig:
    source_root = tmp_path / "source"
    source_root.mkdir()
    (source_root / ".git").mkdir()
    task_bin = tmp_path / "task.exe"
    git_bin = tmp_path / "git.exe"
    task_bin.touch()
    git_bin.touch()
    return runner.RunnerConfig(
        source_root=source_root,
        task_bin=task_bin,
        git_bin=git_bin,
        project_prefix="dropmong-g009-internal-regression",
    )


def completed(
    argv: tuple[str, ...],
    returncode: int = 0,
    stdout: str = "",
    stderr: str = "",
) -> subprocess.CompletedProcess[str]:
    return subprocess.CompletedProcess(argv, returncode, stdout, stderr)


def test_root_taskfile_exposes_both_internal_regression_wrappers() -> None:
    taskfile = (REPOSITORY_ROOT / "Taskfile.yml").read_text(encoding="utf-8")
    assert "  purchase-e2e-with-log-correlation:" in taskfile
    assert "task: tests:purchase-e2e-with-log-correlation" in taskfile
    assert "  purchase-internal-regression:" in taskfile
    assert "{{.TASKFILE_DIR}}/tests/e2e/scripts/run_purchase_internal_regression.py" in taskfile
    assert "INTERNAL_REGRESSION_SOURCE_ROOT: '{{.TASKFILE_DIR}}'" in taskfile
    internal_task = taskfile.split("  purchase-internal-regression:", 1)[1]
    assert "bash" not in internal_task.split("\n  ", 1)[0].lower()


def test_gates_have_exact_order_projects_and_zero_ports(tmp_path: Path) -> None:
    config = make_config(tmp_path)
    run_prefix = "dropmong-g009-internal-regression-a1b2c3d4"
    gates = runner.build_gates(run_prefix)
    assert tuple(gate.task_name for gate in gates) == EXPECTED_TASKS
    assert dict(gates[0].variables)["SERVICES"] == (
        "catalog-service order-service payment-service notification-service"
    )
    project_values: list[str] = []
    project_keys = (
        None,
        "E2E_COMPOSE_PROJECT",
        "PURCHASE_CONCURRENCY_COMPOSE_PROJECT",
        "PAYMENT_FAILURE_IDEMPOTENCY_COMPOSE_PROJECT",
        "E2E_COMPOSE_PROJECT",
        "E2E_COMPOSE_PROJECT",
        "E2E_COMPOSE_PROJECT",
        "E2E_COMPOSE_PROJECT",
    )
    for gate, project_key in zip(gates, project_keys, strict=True):
        variables = dict(gate.variables)
        assert all(variables[name] == "0" for name in runner.PORT_VARIABLES)
        argv = runner.gate_argv(config.task_bin, gate)
        assert argv[:2] == (str(config.task_bin), gate.task_name)
        if project_key is not None:
            project_values.append(variables[project_key])
    assert len(project_values) == len(set(project_values)) == 7
    suffixes = (
        "metrics", "concurrency", "payment-failure", "traces", "kafka-traces",
        "log-correlation", "notification-metrics",
    )
    expected_projects = tuple(f"{run_prefix}-{suffix}" for suffix in suffixes)
    assert tuple(project_values) == expected_projects


def test_run_process_uses_argument_array_without_shell(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    observed: dict[str, object] = {}

    def fake_run(
        argv: tuple[str, ...],
        **kwargs: object,
    ) -> subprocess.CompletedProcess[str]:
        observed["argv"] = argv
        observed.update(kwargs)
        return completed(argv)
    monkeypatch.setattr(subprocess, "run", fake_run)
    runner.run_process(("task.exe", "test-services"), cwd=Path("clone"), env={})
    assert observed["argv"] == ("task.exe", "test-services")
    assert observed["shell"] is False
    assert observed["check"] is False
    assert observed["capture_output"] is True
    assert observed["text"] is True
    assert (observed["encoding"], observed["errors"]) == ("utf-8", "replace")


def test_run_gates_fails_fast_and_preserves_output_and_code(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    config = make_config(tmp_path)
    gates = runner.build_gates("dropmong-g009-internal-regression-a1b2c3d4")
    calls: list[tuple[str, ...]] = []

    def fake_process(
        argv: tuple[str, ...],
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        calls.append(argv)
        assert cwd == tmp_path / "clone"
        assert env is not None
        assert all(env[name] == "0" for name in runner.PORT_VARIABLES)
        if len(calls) == 2:
            return completed(argv, 23, "failed stdout\n", "failed stderr\n")
        return completed(argv, stdout="passed\n")
    monkeypatch.setattr(runner, "run_process", fake_process)
    with pytest.raises(runner.RunnerExecutionError) as error:
        runner.run_gates(config, tmp_path / "clone", gates)
    assert error.value.exit_code == 23
    assert len(calls) == 2
    output = capsys.readouterr()
    assert "passed" in output.out
    assert "failed stdout" in output.out
    assert "failed stderr" in output.err


def test_clone_uses_safe_directories_and_no_hardlinks(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    config = make_config(tmp_path)
    observed: list[tuple[str, ...]] = []

    def fake_process(
        argv: tuple[str, ...],
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        observed.append(argv)
        return completed(argv)
    monkeypatch.setattr(runner, "run_process", fake_process)
    clone_root = tmp_path / "temporary" / "services"
    runner.clone_committed_head(config, clone_root)
    argv = observed[0]
    assert argv[:5] == (
        str(config.git_bin),
        "-c",
        f"safe.directory={config.source_root}",
        "-c",
        f"safe.directory={config.source_root / '.git'}",
    )
    assert argv[5:8] == ("clone", "--no-hardlinks", "--quiet")
    assert argv[-2:] == (str(config.source_root), str(clone_root))


def test_cleanup_failure_does_not_mask_primary_failure(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    config = make_config(tmp_path)
    temporary_root = tmp_path / "temporary"
    temporary_root.mkdir()
    monkeypatch.setattr(
        runner.tempfile,
        "mkdtemp",
        lambda **_kwargs: str(temporary_root),
    )
    monkeypatch.setattr(
        runner,
        "clone_committed_head",
        lambda *_args: (_ for _ in ()).throw(runner.RunnerExecutionError("gate", 29)),
    )
    monkeypatch.setattr(
        runner.shutil,
        "rmtree",
        lambda _path: (_ for _ in ()).throw(OSError("locked")),
    )
    with pytest.raises(runner.RunnerExecutionError) as error:
        runner.execute(config, run_token="a1b2c3d4")
    assert error.value.exit_code == 29
    assert "cleanup failed after original failure" in caplog.text


@pytest.mark.parametrize("value", ("", "UPPER", "has space", "../escape", "-leading"))
def test_invalid_project_prefix_is_rejected(value: str) -> None:
    with pytest.raises(runner.RunnerInputError):
        runner.validate_project_prefix(value)


def test_invalid_task_override_and_source_root_are_rejected(tmp_path: Path) -> None:
    source_root = tmp_path / "source"
    source_root.mkdir()
    (source_root / ".git").mkdir()
    environment = {
        "INTERNAL_REGRESSION_SOURCE_ROOT": str(source_root),
        "INTERNAL_REGRESSION_TASK_BIN": str(tmp_path / "missing-task.exe"),
    }
    with pytest.raises(runner.RunnerInputError, match="task executable"):
        runner.config_from_env(environment)
    environment["INTERNAL_REGRESSION_SOURCE_ROOT"] = str(tmp_path / "missing-source")
    with pytest.raises(runner.RunnerInputError, match="source root"):
        runner.config_from_env(environment)


def test_internal_only_message_is_explicit(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    config = make_config(tmp_path)
    monkeypatch.setattr(
        runner,
        "clone_committed_head",
        lambda *_args: tmp_path / "clone",
    )
    monkeypatch.setattr(runner, "run_gates", lambda *_args: None)
    runner.execute(config, run_token="a1b2c3d4")
    message = "Gateway JWT is excluded; this regression is internal only."
    assert message in capsys.readouterr().out
    assert not (tmp_path / "clone").exists()
