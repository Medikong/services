from __future__ import annotations

import logging
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import replace
from pathlib import Path

from payment_failure_idempotency_runner_support import (
    CleanupFailure,
    ComposeConfig,
    RunnerConfig,
    RunnerExecutionError,
    RunnerInputError,
    SmokeResult,
    compose_argv,
    config_from_env,
    create_tracked_context,
    parse_compose_command,
    run_process,
    validate_name,
    validate_positive_integer,
    validate_smoke_image,
)
from payment_failure_idempotency_scenario_support import _database_checks
from payment_failure_idempotency_smoke_support import parse_smoke_json


__all__ = (
    "CleanupFailure",
    "ComposeConfig",
    "RunnerConfig",
    "RunnerInputError",
    "cleanup",
    "compose_argv",
    "create_tracked_context",
    "execute",
    "parse_compose_command",
    "parse_smoke_json",
    "run_scenario",
    "validate_name",
    "validate_positive_integer",
    "validate_smoke_image",
)
LOGGER = logging.getLogger(__name__)
SMOKE_SERVICE = "payment-failure-idempotency-smoke"
APPLICATION_SERVICES = (
    "postgres",
    "kafka",
    "kafka-init",
    "order-service",
    "payment-service",
)


def _environment(config: RunnerConfig) -> dict[str, str]:
    environment = dict(os.environ)
    environment["PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID"] = config.run_id
    environment["PAYMENT_FAILURE_IDEMPOTENCY_TIMEOUT_SECONDS"] = str(
        config.scenario_timeout_seconds,
    )
    if config.smoke_image is None:
        environment.pop("PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE", None)
    else:
        environment["PAYMENT_FAILURE_IDEMPOTENCY_SMOKE_IMAGE"] = config.smoke_image
    return environment


def _require_success(
    argv: tuple[str, ...],
    environment: dict[str, str],
) -> subprocess.CompletedProcess[str]:
    result = run_process(argv, env=environment)
    if result.returncode != 0:
        details = result.stderr.strip() or result.stdout.strip() or "no output"
        exit_code = result.returncode if 0 < result.returncode < 256 else 1
        raise RunnerExecutionError(
            f"command failed ({result.returncode}): "
            f"{subprocess.list2cmdline(argv)}\n{details}",
            exit_code,
        )
    return result


def run_scenario(config: RunnerConfig) -> None:
    """Start the stack, run the smoke service, and assert database state."""
    environment = _environment(config)
    _require_success(
        compose_argv(config.compose, ("down", "-v", "--remove-orphans")),
        environment,
    )
    _require_success(
        compose_argv(
            config.compose,
            (
                "up",
                "-d",
                "--build",
                "--wait",
                "--wait-timeout",
                str(config.wait_timeout_seconds),
                *APPLICATION_SERVICES,
            ),
        ),
        environment,
    )
    _require_success(
        compose_argv(config.compose, ("build", SMOKE_SERVICE)),
        environment,
    )
    smoke_process = _require_success(
        compose_argv(config.compose, ("run", "--rm", "--no-deps", "-T", SMOKE_SERVICE)),
        environment,
    )
    smoke_json = smoke_process.stdout.strip()
    print(smoke_json)
    smoke = parse_smoke_json(smoke_json, config.run_id)
    failures: list[str] = []
    for check in _database_checks(config, smoke):
        result = run_process(
            compose_argv(
                config.compose,
                (
                    "exec",
                    "-T",
                    "postgres",
                    "psql",
                    "-U",
                    "app",
                    "-d",
                    check.database,
                    "-At",
                    "-F",
                    "|",
                    "-c",
                    check.query,
                ),
            ),
            env=environment,
        )
        actual = result.stdout.strip()
        print(f"{check.label}={actual}")
        if result.returncode != 0 or actual != check.expected:
            reported = actual or result.stderr.strip() or "no output"
            failures.append(
                f"{check.failure_label} failed: expected {check.expected}, got {reported}",
            )
    if failures:
        raise RunnerExecutionError("\n".join(failures))


def cleanup(
    compose: ComposeConfig,
    temporary_context: Path,
    environment: dict[str, str],
) -> CleanupFailure | None:
    """Remove Compose resources and the isolated context, collecting failures."""
    messages: list[str] = []
    try:
        result = run_process(
            compose_argv(compose, ("down", "-v", "--remove-orphans")),
            env=environment,
        )
    except RunnerExecutionError as exc:
        messages.append(f"compose cleanup failed: {exc}")
    else:
        if result.returncode != 0:
            details = result.stderr.strip() or result.stdout.strip() or "no output"
            messages.append(f"compose cleanup failed ({result.returncode}): {details}")
    try:
        shutil.rmtree(temporary_context)
    except OSError as exc:
        messages.append(f"temporary context cleanup failed: {exc}")
    return CleanupFailure(tuple(messages)) if messages else None


def execute(config: RunnerConfig) -> None:
    """Execute the scenario with cleanup that preserves any primary failure."""
    temporary_context = Path(tempfile.mkdtemp(prefix="payment-failure-idempotency-"))
    runtime_config = config
    environment = _environment(config)
    try:
        create_tracked_context(config.source_root, temporary_context)
        runtime_config = replace(
            config,
            compose=replace(
                config.compose,
                compose_file=temporary_context / config.compose_relative,
            ),
        )
        run_scenario(runtime_config)
    finally:
        primary_failure = sys.exception()
        cleanup_compose = (
            runtime_config.compose
            if runtime_config.compose.compose_file.is_file()
            else config.compose
        )
        cleanup_failure = cleanup(cleanup_compose, temporary_context, environment)
        if cleanup_failure is not None:
            if primary_failure is None:
                raise cleanup_failure
            LOGGER.error("cleanup failed after original failure: %s", cleanup_failure)


def main() -> int:
    """Run the payment-failure idempotency E2E command."""
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
        LOGGER.error("cleanup failed: %s", exc)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
