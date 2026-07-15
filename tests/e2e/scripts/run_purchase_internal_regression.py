from __future__ import annotations

import logging
import os
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path
from typing import TextIO

from purchase_internal_regression_cleanup import (
    CleanupFailure,
    CleanupIO,
    cleanup_resources,
)

from purchase_internal_regression_support import (
    PORT_VARIABLES,
    Gate,
    RunnerConfig,
    RunnerInputError,
    build_gates,
    config_from_env,
    gate_argv,
    parse_start_gate,
    validate_project_prefix,
)

LOGGER = logging.getLogger(__name__)


class RunnerExecutionError(RuntimeError):
    """Report an external command failure with its process exit code."""

    def __init__(self, message: str, exit_code: int = 1) -> None:
        super().__init__(message)
        self.exit_code = exit_code if 0 < exit_code < 256 else 1


def run_process(
    argv: tuple[str, ...],
    *,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run one captured process without a command shell."""
    try:
        return subprocess.run(
            argv,
            cwd=cwd,
            env=env,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            shell=False,
        )
    except OSError as exc:
        raise RunnerExecutionError(
            f"unable to start command: {subprocess.list2cmdline(argv)}: {exc}",
        ) from exc


def _emit_text(stream: TextIO, value: str) -> None:
    try:
        buffer = stream.buffer
    except AttributeError:
        encoding = getattr(stream, "encoding", None) or "utf-8"
        safe_value = value.encode(encoding, errors="replace").decode(encoding)
        stream.write(safe_value)
        stream.flush()
    else:
        buffer.write(value.encode("utf-8"))
        buffer.flush()


def _emit_output(result: subprocess.CompletedProcess[str]) -> None:
    if result.stdout:
        _emit_text(sys.stdout, result.stdout)
    if result.stderr:
        _emit_text(sys.stderr, result.stderr)


def clone_committed_head(config: RunnerConfig, clone_root: Path) -> None:
    """Clone committed HEAD into an isolated directory without hardlinks."""
    argv = (
        str(config.git_bin),
        "-c",
        f"safe.directory={config.source_root}",
        "-c",
        f"safe.directory={config.git_dir}",
        "clone",
        "--no-hardlinks",
        "--quiet",
        str(config.source_root),
        str(clone_root),
    )
    result = run_process(argv)
    _emit_output(result)
    if result.returncode != 0:
        raise RunnerExecutionError("failed to clone committed HEAD", result.returncode)


def run_gates(config: RunnerConfig, clone_root: Path, gates: tuple[Gate, ...]) -> None:
    """Run gates in order and stop at the first failed Task process."""
    for index, gate in enumerate(gates, start=1):
        argv = gate_argv(config.task_bin, gate)
        print(
            f"[{index}/{len(gates)}] {gate.label}: {subprocess.list2cmdline(argv)}",
            flush=True,
        )
        environment = dict(os.environ)
        environment.update(gate.variables)
        started_at = time.monotonic()
        result = run_process(argv, cwd=clone_root, env=environment)
        duration_seconds = time.monotonic() - started_at
        _emit_output(result)
        print(
            f"gate={gate.task_name} duration_seconds={duration_seconds:.3f} "
            f"exit_code={result.returncode}",
            flush=True,
        )
        if result.returncode != 0:
            raise RunnerExecutionError(
                f"gate failed: {gate.task_name}",
                result.returncode,
            )


def execute(config: RunnerConfig, *, run_token: str | None = None) -> None:
    """Clone committed HEAD, run all gates, and always remove the clone."""
    token = run_token or uuid.uuid4().hex[:8]
    run_prefix = f"{config.project_prefix}-{token}"
    gates = build_gates(run_prefix)[config.start_gate - 1 :]
    temporary_root = Path(tempfile.mkdtemp(prefix="purchase-internal-regression-"))
    clone_root = temporary_root / "services"
    print("Gateway JWT is excluded; this regression is internal only.", flush=True)
    try:
        clone_committed_head(config, clone_root)
        run_gates(config, clone_root, gates)
    finally:
        primary_failure = sys.exception()
        cleanup_failure: CleanupFailure | None = None
        try:
            cleanup_resources(config, gates, CleanupIO(run_process, _emit_output))
        except CleanupFailure as exc:
            cleanup_failure = exc
        try:
            shutil.rmtree(temporary_root)
        except OSError as exc:
            cleanup_failure = CleanupFailure(f"temporary clone cleanup failed: {exc}")
        if cleanup_failure is not None:
            if primary_failure is None:
                raise cleanup_failure
            LOGGER.error("cleanup failed after original failure: %s", cleanup_failure)
        else:
            print("cleanup remaining temp_clones=0", flush=True)


def main() -> int:
    """Run the Gateway-excluded internal purchase regression."""
    logging.basicConfig(level=logging.INFO, format="%(levelname)s: %(message)s")
    try:
        execute(config_from_env(os.environ))
    except RunnerInputError as exc:
        LOGGER.error("%s", exc)
        return 2
    except RunnerExecutionError as exc:
        LOGGER.error("%s", exc)
        return exc.exit_code
    except CleanupFailure as exc:
        LOGGER.error("%s", exc)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
