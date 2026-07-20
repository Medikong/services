from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SERVICES = (
    "catalog-service",
    "interest-service",
    "notification-service",
    "order-service",
    "payment-service",
)


@pytest.mark.parametrize(
    ("output_format", "expected"),
    (
        ("shell", f"{' '.join(SERVICES)}\n".encode("utf-8")),
        (
            "lines",
            "".join(f"{service}\n" for service in SERVICES).encode("utf-8"),
        ),
        ("json", f"{json.dumps(SERVICES)}\n".encode("utf-8")),
    ),
)
def test_cli_stdout_is_exact_utf8_lf_bytes(
    output_format: str,
    expected: bytes,
) -> None:
    result = subprocess.run(
        (
            "sh",
            "scripts/list_services.sh",
            "list",
            "--mode",
            "tests",
            "--format",
            output_format,
        ),
        cwd=REPOSITORY_ROOT,
        check=False,
        capture_output=True,
    )

    assert result.returncode == 0
    assert result.stderr == b""
    assert result.stdout == expected
    assert b"\r" not in result.stdout
