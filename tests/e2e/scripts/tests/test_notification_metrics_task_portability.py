from __future__ import annotations

from pathlib import Path


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def test_notification_metrics_task_uses_python_for_poll_delay() -> None:
    taskfile = (SERVICE_ROOT / "tests" / "Taskfile.yml").read_text(encoding="utf-8")
    task = taskfile.split("  purchase-e2e-with-notification-metrics:", 1)[1].split(
        "\n  purchase-e2e-concurrency:",
        1,
    )[0]
    stripped_lines = {line.strip() for line in task.splitlines()}

    assert "sleep 2" not in stripped_lines
    assert "python_bin='{{.PYTHON_BIN}}'" in task
    assert '"${python_bin}" -c \'import time; time.sleep(2)\'' in task
