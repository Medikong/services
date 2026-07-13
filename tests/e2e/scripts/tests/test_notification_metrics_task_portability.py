from __future__ import annotations

from pathlib import Path


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def task_body(task_name: str, next_task_name: str) -> str:
    taskfile = (SERVICE_ROOT / "tests" / "Taskfile.yml").read_text(encoding="utf-8")
    return taskfile.split(f"  {task_name}:", 1)[1].split(
        f"\n  {next_task_name}:",
        1,
    )[0]


def test_notification_metrics_task_uses_python_for_poll_delay() -> None:
    task = task_body(
        "purchase-e2e-with-notification-metrics",
        "purchase-e2e-concurrency",
    )
    stripped_lines = {line.strip() for line in task.splitlines()}

    assert "sleep 2" not in stripped_lines
    assert "python_bin='{{.PYTHON_BIN}}'" in task
    assert '"${python_bin}" -c \'import time; time.sleep(2)\'' in task


def test_log_and_notification_contexts_are_unique_and_cleanup_clone_failures() -> None:
    task_pairs = (
        ("purchase-e2e-with-log-correlation", "purchase-e2e-with-notification-metrics"),
        ("purchase-e2e-with-notification-metrics", "purchase-e2e-concurrency"),
    )
    for task_name, next_task_name in task_pairs:
        task = task_body(task_name, next_task_name)
        assert "uuid.uuid4().hex" in task
        assert "context-${run_token}" in task
        assert task.index("trap '") < task.index("git clone --no-hardlinks")
        assert 'rm -rf "${context}"\n        git clone' not in task


def test_e2e_host_ports_bind_only_to_loopback() -> None:
    compose = (SERVICE_ROOT / "tests" / "e2e" / "docker-compose.yml").read_text(
        encoding="utf-8"
    )
    published_ports = [
        line.strip().removeprefix('- "').removesuffix('"')
        for line in compose.splitlines()
        if line.strip().startswith('- "') and line.strip().endswith('"') and ":" in line
    ]
    assert published_ports
    assert all(port.startswith("127.0.0.1:") for port in published_ports)
