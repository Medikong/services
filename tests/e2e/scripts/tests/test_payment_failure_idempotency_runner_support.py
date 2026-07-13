from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import pytest


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from run_payment_failure_idempotency import (  # noqa: E402
    ComposeConfig,
    RunnerInputError,
    compose_argv,
    create_tracked_context,
    parse_compose_command,
    parse_smoke_json,
    run_process,
    validate_name,
    validate_positive_integer,
    validate_smoke_image,
)


def test_compose_argv_keeps_windows_paths_as_one_argument() -> None:
    config = ComposeConfig(
        command=("docker", "compose"),
        project="dropmong-payment-green",
        compose_file=Path(r"D:\build root\tests\e2e\docker-compose.yml"),
    )

    assert compose_argv(config, ("up", "-d", "postgres")) == (
        "docker",
        "compose",
        "-p",
        "dropmong-payment-green",
        "-f",
        r"D:\build root\tests\e2e\docker-compose.yml",
        "up",
        "-d",
        "postgres",
    )


@pytest.mark.parametrize("value", ["", "dropmong/payment", "dropmong payment"])
def test_validate_name_rejects_compose_project_injection(value: str) -> None:
    with pytest.raises(RunnerInputError):
        validate_name("PAYMENT_FAILURE_IDEMPOTENCY_PROJECT", value)


def test_input_parsers_accept_supported_compose_commands_and_positive_timeout() -> None:
    assert parse_compose_command("docker compose") == ("docker", "compose")
    assert parse_compose_command("docker-compose") == ("docker-compose",)
    assert validate_positive_integer("TIMEOUT", "180") == 180
    assert validate_smoke_image("") is None
    assert validate_smoke_image("python:3.12-slim") == "python:3.12-slim"


def test_input_parsers_reject_unsupported_compose_command_and_zero_timeout() -> None:
    with pytest.raises(RunnerInputError):
        parse_compose_command("bash -c docker compose")
    with pytest.raises(RunnerInputError):
        validate_positive_integer("TIMEOUT", "0")


def test_run_process_preserves_utf8_output_invalid_in_cp949() -> None:
    output = "docker output " + chr(0x1F600)
    script = (
        "import sys; "
        "output = 'docker output ' + chr(0x1F600); "
        "sys.stdout.buffer.write(output.encode('utf-8')); "
        "sys.stderr.buffer.write(output.encode('utf-8'))"
    )

    result = run_process((sys.executable, "-c", script))

    assert result.returncode == 0
    assert result.stdout == output
    assert result.stderr == output


@pytest.mark.parametrize(
    "value",
    ["-python:3.12", "python:3.12;docker", "python image:latest"],
)
def test_validate_smoke_image_rejects_command_or_argument_injection(value: str) -> None:
    with pytest.raises(RunnerInputError):
        validate_smoke_image(value)


def test_create_tracked_context_omits_ignored_pytest_cache(tmp_path: Path) -> None:
    source = tmp_path / "source"
    destination = tmp_path / "context"
    source.mkdir()
    subprocess.run(
        ["git", "init", "--quiet"],
        cwd=source,
        check=True,
        capture_output=True,
        text=True,
    )
    (source / ".gitignore").write_text(".pytest_cache/\n", encoding="utf-8")
    (source / "tracked.txt").write_text("tracked worktree content", encoding="utf-8")
    ignored_cache = source / ".pytest_cache" / "locked"
    ignored_cache.mkdir(parents=True)
    (ignored_cache / "nodeids").write_text("must not be copied", encoding="utf-8")
    subprocess.run(
        ["git", "add", ".gitignore", "tracked.txt"],
        cwd=source,
        check=True,
        capture_output=True,
        text=True,
    )

    create_tracked_context(source, destination)

    assert (destination / "tracked.txt").read_text(encoding="utf-8") == (
        "tracked worktree content"
    )
    assert not (destination / ".pytest_cache").exists()


def test_parse_smoke_json_returns_validated_database_values() -> None:
    run_id = "payment-failure-idempotency-20260713T010203Z-42"
    payload = json.dumps(
        {
            "ok": True,
            "order_id": "order-1",
            "payment_id": "payment-1",
            "run_id": run_id,
            "unique_event_ids": ["event-1"],
            "user_id": f"{run_id}-user",
        },
    )

    result = parse_smoke_json(payload, run_id)

    assert result.order_id == "order-1"
    assert result.payment_id == "payment-1"
    assert result.event_id == "event-1"


def test_parse_smoke_json_rejects_unsafe_sql_value() -> None:
    run_id = "payment-failure-idempotency-20260713T010203Z-42"
    payload = json.dumps(
        {
            "ok": True,
            "order_id": "order-1';DROP_TABLE_orders;--",
            "payment_id": "payment-1",
            "run_id": run_id,
            "unique_event_ids": ["event-1"],
            "user_id": f"{run_id}-user",
        },
    )

    with pytest.raises(RunnerInputError):
        parse_smoke_json(payload, run_id)
