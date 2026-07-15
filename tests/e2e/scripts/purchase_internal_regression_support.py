from __future__ import annotations

import re
import shutil
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path

DEFAULT_PROJECT_PREFIX = "dropmong-g009-internal-regression"
SERVICE_NAMES = "catalog-service order-service payment-service notification-service"
UNIT_PYTEST_ARGS = (
    "-q -s -p no:cacheprovider --ignore=tests/integration "
    "--ignore=tests/test_migrations.py --ignore=tests/test_order_expiry_migration.py "
    "--ignore=tests/test_catalog_postgres.py "
    "--ignore=tests/test_inventory_projection_postgres.py"
)
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
TOTAL_GATES = 11


class RunnerInputError(ValueError):
    """Report invalid runner input before external commands start."""


@dataclass(frozen=True, slots=True)
class RunnerConfig:
    """Hold validated repository and executable inputs."""

    source_root: Path
    git_dir: Path
    task_bin: Path
    git_bin: Path
    docker_bin: Path
    project_prefix: str
    start_gate: int


@dataclass(frozen=True, slots=True)
class Gate:
    """Describe one ordered Task invocation and its variables."""

    label: str
    task_name: str
    variables: tuple[tuple[str, str], ...]
    cleanup_projects: tuple[str, ...]


def validate_project_prefix(value: str) -> str:
    """Return a Compose-safe project prefix or raise RunnerInputError."""
    if PROJECT_PREFIX_PATTERN.fullmatch(value) is None:
        raise RunnerInputError(
            "project prefix must start with a lowercase letter or digit and contain "
            "only lowercase letters, digits, underscores, or hyphens",
        )
    return value


def parse_start_gate(value: str) -> int:
    try:
        start_gate = int(value)
    except ValueError as exc:
        raise RunnerInputError(f"start gate must be an integer: {value}") from exc
    if not 1 <= start_gate <= TOTAL_GATES:
        raise RunnerInputError(
            f"start gate must be between 1 and {TOTAL_GATES}: {start_gate}",
        )
    return start_gate


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


def _resolve_git_dir(source_root: Path) -> Path:
    marker = source_root / ".git"
    if marker.is_dir():
        return marker.resolve(strict=True)
    try:
        marker_text = marker.read_text(encoding="utf-8").strip()
    except OSError as exc:
        raise RunnerInputError(
            f"Git directory marker is unreadable: {marker}",
        ) from exc
    prefix = "gitdir: "
    if not marker_text.startswith(prefix):
        raise RunnerInputError(f"Git directory marker is invalid: {marker}")
    candidate = Path(marker_text.removeprefix(prefix))
    if not candidate.is_absolute():
        candidate = marker.parent / candidate
    try:
        git_dir = candidate.resolve(strict=True)
    except (OSError, RuntimeError) as exc:
        raise RunnerInputError(f"Git directory is invalid: {candidate}") from exc
    if not git_dir.is_dir():
        raise RunnerInputError(f"Git directory is not a directory: {git_dir}")
    return git_dir


def config_from_env(environment: Mapping[str, str]) -> RunnerConfig:
    """Validate environment input and resolve Task and Git executables."""
    source_root = _resolve_source_root(
        environment.get("INTERNAL_REGRESSION_SOURCE_ROOT", ""),
    )
    git_dir = _resolve_git_dir(source_root)
    task_override = environment.get("INTERNAL_REGRESSION_TASK_BIN", "").strip()
    task_bin = _resolve_executable(task_override or "task", "task")
    git_bin = _resolve_executable("git", "git")
    docker_bin = _resolve_executable("docker", "docker")
    project_prefix = validate_project_prefix(
        environment.get(
            "INTERNAL_REGRESSION_PROJECT_PREFIX",
            DEFAULT_PROJECT_PREFIX,
        ).strip(),
    )
    start_gate = parse_start_gate(
        environment.get("INTERNAL_REGRESSION_START_GATE", "1").strip(),
    )
    return RunnerConfig(
        source_root,
        git_dir,
        task_bin,
        git_bin,
        docker_bin,
        project_prefix,
        start_gate,
    )


def _gate(
    label: str,
    task_name: str,
    run_prefix: str,
    project: tuple[str, str] | None = None,
    variables: tuple[tuple[str, str], ...] = (),
    cleanup_projects: tuple[str, ...] = (),
) -> Gate:
    project_variables = ()
    if project is not None:
        project_name = f"{run_prefix}-{project[1]}"
        project_variables = ((project[0], project_name),)
        if project[0] == "E2E_COMPOSE_PROJECT":
            project_variables += (("E2E_NETWORK", f"{project_name}_default"),)
    ports = tuple((name, "0") for name in PORT_VARIABLES)
    image_prefix = (("TEST_RUNNER_IMAGE_PREFIX", f"{run_prefix}-test-runner"),)
    return Gate(
        label,
        task_name,
        (*variables, *project_variables, *image_prefix, *ports),
        cleanup_projects,
    )


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
            variables=(
                ("SERVICES", SERVICE_NAMES),
                ("PYTEST_ARGS", UNIT_PYTEST_ARGS),
            ),
        ),
        _gate("purchase contracts", "test-purchase-contracts", run_prefix),
        _gate(
            "purchase PostgreSQL integration",
            "test-purchase-postgres-integration",
            run_prefix,
            ("PURCHASE_POSTGRES_COMPOSE_PROJECT", "postgres"),
            cleanup_projects=(f"{run_prefix}-postgres",),
        ),
        _gate(
            "purchase metrics",
            "purchase-e2e-with-metrics",
            run_prefix,
            (e2e, "metrics"),
            cleanup_projects=(f"{run_prefix}-metrics",),
        ),
        _gate(
            "purchase concurrency",
            "purchase-e2e-concurrency",
            run_prefix,
            ("PURCHASE_CONCURRENCY_COMPOSE_PROJECT", "concurrency"),
            cleanup_projects=(f"{run_prefix}-concurrency",),
        ),
        _gate(
            "payment failure idempotency",
            "payment-failure-idempotency",
            run_prefix,
            ("PAYMENT_FAILURE_IDEMPOTENCY_COMPOSE_PROJECT", "payment-failure"),
            cleanup_projects=(f"{run_prefix}-payment-failure",),
        ),
        _gate(
            "durable purchase lifecycle",
            "purchase-lifecycle-e2e",
            run_prefix,
            ("PURCHASE_LIFECYCLE_COMPOSE_PREFIX", "lifecycle"),
            cleanup_projects=(
                f"{run_prefix}-lifecycle-postgres",
                f"{run_prefix}-lifecycle-metrics",
                f"{run_prefix}-lifecycle-notifications-notification-metrics",
            ),
        ),
        _gate(
            "purchase traces",
            "purchase-e2e-with-traces",
            run_prefix,
            (e2e, "traces"),
            cleanup_projects=(f"{run_prefix}-traces-traces",),
        ),
        _gate(
            "purchase Kafka traces",
            "purchase-e2e-with-kafka-traces",
            run_prefix,
            (e2e, "kafka-traces"),
            cleanup_projects=(f"{run_prefix}-kafka-traces",),
        ),
        _gate(
            "purchase log correlation",
            "purchase-e2e-with-log-correlation",
            run_prefix,
            (e2e, "log-correlation"),
            cleanup_projects=(f"{run_prefix}-log-correlation-logs",),
        ),
        _gate(
            "notification metrics",
            "purchase-e2e-with-notification-metrics",
            run_prefix,
            (e2e, "notification-metrics"),
            cleanup_projects=(
                f"{run_prefix}-notification-metrics-notification-metrics",
            ),
        ),
    )


def gate_argv(task_bin: Path, gate: Gate) -> tuple[str, ...]:
    """Create a shell-free Task argument array for one gate."""
    assignments = tuple(f"{name}={value}" for name, value in gate.variables)
    return (str(task_bin), gate.task_name, *assignments)
