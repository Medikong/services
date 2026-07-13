from __future__ import annotations

import os
import re
import shutil
import subprocess
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Final


NAME_PATTERN: Final = re.compile(r"[A-Za-z0-9._-]+")
IMAGE_PATTERN: Final = re.compile(r"[A-Za-z0-9][A-Za-z0-9._/:@-]*")


@dataclass(frozen=True, slots=True)
class RunnerInputError(Exception):
    """Report invalid runner configuration or untrusted smoke output."""

    message: str

    def __str__(self) -> str:
        return self.message


@dataclass(frozen=True, slots=True)
class RunnerExecutionError(Exception):
    """Report an operational runner failure with its intended exit code."""

    message: str
    exit_code: int = 1

    def __str__(self) -> str:
        return self.message


@dataclass(frozen=True, slots=True)
class CleanupFailure(Exception):
    """Collect cleanup errors without masking an active scenario failure."""

    messages: tuple[str, ...]

    def __str__(self) -> str:
        return "; ".join(self.messages)


@dataclass(frozen=True, slots=True)
class ComposeConfig:
    """Hold the validated Compose executable and project coordinates."""

    command: tuple[str, ...]
    project: str
    compose_file: Path


@dataclass(frozen=True, slots=True)
class RunnerConfig:
    """Hold validated scenario configuration."""

    source_root: Path
    compose_relative: Path
    compose: ComposeConfig
    wait_timeout_seconds: int
    scenario_timeout_seconds: int
    smoke_image: str | None
    run_id: str


@dataclass(frozen=True, slots=True)
class SmokeResult:
    """Hold smoke identifiers that are safe to interpolate into SQL."""

    order_id: str
    payment_id: str
    user_id: str
    event_id: str


def validate_name(label: str, value: str) -> str:
    """Return a value limited to the shell- and SQL-safe name alphabet."""
    if NAME_PATTERN.fullmatch(value) is None:
        raise RunnerInputError(f"{label} must match [A-Za-z0-9._-]+: {value}")
    return value


def validate_positive_integer(label: str, value: str) -> int:
    """Parse a decimal integer greater than zero."""
    if re.fullmatch(r"[1-9][0-9]*", value) is None:
        raise RunnerInputError(f"{label} must be a positive integer: {value}")
    return int(value)


def validate_smoke_image(value: str) -> str | None:
    """Parse an optional container base image reference."""
    if not value:
        return None
    if IMAGE_PATTERN.fullmatch(value) is None:
        raise RunnerInputError(
            "PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE must be a container image "
            f"reference and cannot begin with -: {value}",
        )
    return value


def parse_compose_command(value: str) -> tuple[str, ...]:
    """Parse the two explicitly supported Compose command forms."""
    commands = {
        "docker compose": ("docker", "compose"),
        "docker-compose": ("docker-compose",),
    }
    try:
        return commands[value]
    except KeyError as exc:
        raise RunnerInputError(
            "DOCKER_COMPOSE must be exactly 'docker compose' or 'docker-compose'",
        ) from exc


def config_from_env(environment: Mapping[str, str]) -> RunnerConfig:
    """Parse runner configuration from the Task-provided environment."""
    names = (
        "PAYMENT_FAILURE_IDEMPOTENCY_ROOT_DIR",
        "PAYMENT_FAILURE_IDEMPOTENCY_DOCKER_COMPOSE",
        "PAYMENT_FAILURE_IDEMPOTENCY_PROJECT",
        "PAYMENT_FAILURE_IDEMPOTENCY_COMPOSE_FILE",
        "PAYMENT_FAILURE_IDEMPOTENCY_WAIT_TIMEOUT_SECONDS",
        "PAYMENT_FAILURE_IDEMPOTENCY_SCENARIO_TIMEOUT_SECONDS",
    )
    missing = tuple(name for name in names if name not in environment)
    if missing:
        raise RunnerInputError(f"missing required environment: {', '.join(missing)}")
    try:
        source_root = Path(environment[names[0]]).resolve(strict=True)
        compose_file = Path(environment[names[3]]).resolve(strict=True)
        compose_relative = compose_file.relative_to(source_root)
    except (OSError, ValueError) as exc:
        raise RunnerInputError(f"invalid project or Compose path: {exc}") from exc
    if not source_root.is_dir() or not compose_file.is_file():
        raise RunnerInputError("project root must be a directory and Compose file a file")
    run_id = validate_name(
        "PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID",
        f"payment-failure-idempotency-{datetime.now(UTC):%Y%m%dT%H%M%SZ}-{os.getpid()}",
    )
    return RunnerConfig(
        source_root=source_root,
        compose_relative=compose_relative,
        compose=ComposeConfig(
            command=parse_compose_command(environment[names[1]]),
            project=validate_name(names[2], environment[names[2]]),
            compose_file=compose_file,
        ),
        wait_timeout_seconds=validate_positive_integer(
            "E2E_WAIT_TIMEOUT_SECONDS", environment[names[4]],
        ),
        scenario_timeout_seconds=validate_positive_integer(
            "PAYMENT_FAILURE_IDEMPOTENCY_SCENARIO_TIMEOUT_SECONDS",
            environment[names[5]],
        ),
        smoke_image=validate_smoke_image(
            environment.get("PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE", ""),
        ),
        run_id=run_id,
    )


def compose_argv(config: ComposeConfig, arguments: Sequence[str]) -> tuple[str, ...]:
    """Build a Compose argument vector without shell parsing."""
    return (
        *config.command,
        "-p",
        config.project,
        "-f",
        str(config.compose_file),
        *arguments,
    )


def run_process(
    argv: tuple[str, ...],
    *,
    cwd: Path | None = None,
    env: Mapping[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run one argument vector and capture its output without a shell."""
    try:
        return subprocess.run(
            argv,
            cwd=cwd,
            env=None if env is None else dict(env),
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
    except OSError as exc:
        raise RunnerExecutionError(
            f"could not launch {subprocess.list2cmdline(argv)}: {exc}",
        ) from exc


def create_tracked_context(source_root: Path, destination: Path) -> None:
    """Copy only Git-indexed worktree files into an isolated build context."""
    tracked = run_process(("git", "ls-files", "-z"), cwd=source_root)
    if tracked.returncode != 0:
        details = tracked.stderr.strip() or tracked.stdout.strip() or "no output"
        raise RunnerExecutionError(f"git ls-files failed: {details}")
    destination.mkdir(parents=True, exist_ok=True)
    for git_path in tracked.stdout.split("\0"):
        if not git_path:
            continue
        relative = Path(*git_path.split("/"))
        if relative.is_absolute() or ".." in relative.parts:
            raise RunnerExecutionError(f"unsafe Git path: {git_path}")
        source = source_root / relative
        target = destination / relative
        if not source.is_file() and not source.is_symlink():
            raise RunnerExecutionError(f"tracked file is unreadable: {source}")
        target.parent.mkdir(parents=True, exist_ok=True)
        try:
            shutil.copy2(source, target, follow_symlinks=False)
        except OSError as exc:
            raise RunnerExecutionError(f"could not copy tracked file {source}: {exc}") from exc
