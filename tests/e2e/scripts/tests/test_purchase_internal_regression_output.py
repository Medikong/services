from __future__ import annotations

import io
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPTS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS_DIR))

import run_purchase_internal_regression as runner


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
        return subprocess.CompletedProcess(argv, 0, "", "")

    monkeypatch.setattr(subprocess, "run", fake_run)
    runner.run_process(("task.exe", "test-services"), cwd=Path("clone"), env={})

    assert observed["argv"] == ("task.exe", "test-services")
    assert observed["shell"] is False
    assert observed["check"] is False
    assert observed["capture_output"] is True
    assert observed["text"] is True
    assert (observed["encoding"], observed["errors"]) == ("utf-8", "replace")


def test_emit_output_preserves_utf8_bytes_on_cp949_console(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    stdout_bytes = io.BytesIO()
    stderr_bytes = io.BytesIO()
    stdout = io.TextIOWrapper(stdout_bytes, encoding="cp949", write_through=True)
    stderr = io.TextIOWrapper(stderr_bytes, encoding="cp949", write_through=True)
    result = subprocess.CompletedProcess(
        ("task.exe",),
        0,
        stdout="passed ✓\n",
        stderr="warning ✓\n",
    )

    with monkeypatch.context() as context:
        context.setattr(runner.sys, "stdout", stdout)
        context.setattr(runner.sys, "stderr", stderr)
        runner._emit_output(result)

    assert stdout_bytes.getvalue() == "passed ✓\n".encode("utf-8")
    assert stderr_bytes.getvalue() == "warning ✓\n".encode("utf-8")
