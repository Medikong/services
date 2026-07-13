from __future__ import annotations

from pathlib import Path


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def test_purchase_kafka_trace_task_uses_posix_case_validation() -> None:
    taskfile = (SERVICE_ROOT / "tests" / "Taskfile.yml").read_text(encoding="utf-8")
    task = taskfile.split("  purchase-e2e-with-kafka-traces:", 1)[1].split(
        "\n  purchase-e2e-with-log-correlation:",
        1,
    )[0]

    assert "grep" not in task
    assert '""|*[!A-Za-z0-9._-]*)' in task
    assert '""|*[!0123456789]*|0*)' in task
    assert '""|[!A-Za-z0-9]*|*[!A-Za-z0-9._/:@-]*)' in task
