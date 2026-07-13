from __future__ import annotations

import re
import shutil
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path

DEFAULT_PROJECT_PREFIX = "dropmong-g009-internal-regression"
SERVICE_NAMES = "catalog-service order-service payment-service notification-service"
PORT_VARIABLES = (
    "CATALOG_SERVICE_PORT",
    "ORDER_SERVICE_PORT",
    "PAYMENT_SERVICE_PORT",
    "NOTIFICATION_SERVICE_PORT",
    "TEMPO_PORT",
    "OTEL_COLLECTOR_GRPC_PORT",
    "OTEL_COLLECTOR_HTTP_PORT",
    "OTEL_COLLECTOR_HEALTH_PORT",
    "LOKI_PORT",
    "ALLOY_PORT",
    "GRAFANA_PORT",
)
PROJECT_PREFIX_PATTERN = re.compile(r"[a-z0-9][a-z0-9_-]{0,47}\Z")
PROJECT_NAME_PATTERN = re.compile(r"[a-z0-9][a-z0-9_-]{0,95}\Z")


class RunnerInputError(ValueError):
    """Report invalid runner input before external commands start."""


@dataclass(frozen=True, slots=True)
class RunnerConfig:
    """Hold validated repository and executable inputs."""

    source_root: Path
    task_bin: Path
    git_bin: Path
    project_prefix: str


@dataclass(frozen=True, slots=True)
class Gate:
    """Describe one ordered Task invocation and its variables."""

    label: str
    task_name: str
    variables: tuple[tuple[str, str], ...]


def validate_project_prefix(value: str) -> str:
    """Return a Compose-safe project prefix or raise RunnerInputError."""
    if PROJECT_PREFIX_PATTERN.fullmatch(value) is None:
        raise RunnerInputError(
            "project prefix must start with a lowercase letter or digit and contain "
            "only lowercase letters, digits, underscores, or hyphens",
        )
    return value


def _resolve_source_root(value: str) -> Path:
    try:
        source_root = Path(value).expanduser().resolve(strict=True)
    except (OSError, RuntimeError) as exc:
        raise RunnerInputError(f"source root is invalid: {value}") from exc
    if not source_root.is_dir() or not (source_root / ".git").exists():
        raise RunnerInputError(f"source root is not a Git working tree: {source_root}")
    return source_root


def _resolve_executable(value: str, label: str) -> Path:
    if not value or any(character in value for character in ("\0", "\r", "\n")):
        raise RunnerInputError(f"{label} executable is invalid")
    located = shutil.which(value)
    candidate = Path(located if located is not None else value).expanduser()
    try:
        resolved = candidate.resolve(strict=True)
    except (OSError, RuntimeError) as exc:
        raise RunnerInputError(f"{label} executable was not found: {value}") from exc
    if not resolved.is_file():
        raise RunnerInputError(f"{label} executable is not a file: {resolved}")
    return resolved


def config_from_env(environment: Mapping[str, str]) -> RunnerConfig:
    """Validate environment input and resolve Task and Git executables."""
    source_root = _resolve_source_root(
        environment.get("INTERNAL_REGRESSION_SOURCE_ROOT", ""),
    )
    task_override = environment.get("INTERNAL_REGRESSION_TASK_BIN", "").strip()
    task_bin = _resolve_executable(task_override or "task", "task")
    git_bin = _resolve_executable("git", "git")
    project_prefix = validate_project_prefix(
        environment.get(
            "INTERNAL_REGRESSION_PROJECT_PREFIX",
            DEFAULT_PROJECT_PREFIX,
        ).strip(),
    )
    return RunnerConfig(source_root, task_bin, git_bin, project_prefix)


def _gate(
    label: str,
    task_name: str,
    run_prefix: str,
    project: tuple[str, str] | None = None,
    variables: tuple[tuple[str, str], ...] = (),
) -> Gate:
    project_variables = ()
    if project is not None:
        project_name = f"{run_prefix}-{project[1]}"
        project_variables = ((project[0], project_name),)
        if project[0] == "E2E_COMPOSE_PROJECT":
            project_variables += (("E2E_NETWORK", f"{project_name}_default"),)
    ports = tuple((name, "0") for name in PORT_VARIABLES)
    return Gate(label, task_name, (*variables, *project_variables, *ports))


def build_gates(run_prefix: str) -> tuple[Gate, ...]:
    """Build the required gate sequence with unique Compose projects."""
    if PROJECT_NAME_PATTERN.fullmatch(run_prefix) is None:
        raise RunnerInputError(f"generated project prefix is invalid: {run_prefix}")
    e2e = "E2E_COMPOSE_PROJECT"
    return (
        _gate(
            "service tests",
            "test-services",
            run_prefix,
            variables=(("SERVICES", SERVICE_NAMES),),
        ),
        _gate(
            "purchase metrics",
            "purchase-e2e-with-metrics",
            run_prefix,
            (e2e, "metrics"),
        ),
        _gate(
            "purchase concurrency",
            "purchase-e2e-concurrency",
            run_prefix,
            ("PURCHASE_CONCURRENCY_COMPOSE_PROJECT", "concurrency"),
        ),
        _gate(
            "payment failure idempotency",
            "payment-failure-idempotency",
            run_prefix,
            ("PAYMENT_FAILURE_IDEMPOTENCY_COMPOSE_PROJECT", "payment-failure"),
        ),
        _gate(
            "purchase traces",
            "purchase-e2e-with-traces",
            run_prefix,
            (e2e, "traces"),
        ),
        _gate(
            "purchase Kafka traces",
            "purchase-e2e-with-kafka-traces",
            run_prefix,
            (e2e, "kafka-traces"),
        ),
        _gate(
            "purchase log correlation",
            "purchase-e2e-with-log-correlation",
            run_prefix,
            (e2e, "log-correlation"),
        ),
        _gate(
            "notification metrics",
            "purchase-e2e-with-notification-metrics",
            run_prefix,
            (e2e, "notification-metrics"),
        ),
    )


def gate_argv(task_bin: Path, gate: Gate) -> tuple[str, ...]:
    """Create a shell-free Task argument array for one gate."""
    assignments = tuple(f"{name}={value}" for name, value in gate.variables)
    return (str(task_bin), gate.task_name, *assignments)
