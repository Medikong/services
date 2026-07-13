from __future__ import annotations

from pathlib import Path


SERVICE_ROOT = Path(__file__).resolve().parents[4]


def test_purchase_concurrency_task_uses_portable_shell_validation() -> None:
    taskfile = (SERVICE_ROOT / "tests" / "Taskfile.yml").read_text(encoding="utf-8")
    task = taskfile.split("  purchase-e2e-concurrency:", 1)[1].split(
        "\n  payment-failure-idempotency:",
        1,
    )[0]

    assert "grep" not in task
    assert "$(date " not in task
    assert "date -u" not in task
    assert "PURCHASE_CONCURRENCY_PYTHON_BIN: '{{.PYTHON_BIN}}'" in task
    assert 'python_bin="${PURCHASE_CONCURRENCY_PYTHON_BIN}"' in task
    assert '""|*[!A-Za-z0-9._-]*)' in task
    assert '""|*[!0123456789]*|0*)' in task
    assert '""|[!A-Za-z0-9]*|*[!A-Za-z0-9._/:@-]*)' in task
    assert 'die "${label} must match [A-Za-z0-9._-]+: ${value}"' in task
    assert 'die "${label} must be a positive integer: ${value}"' in task
    assert (
        'die "PURCHASE_CONCURRENCY_SMOKE_IMAGE must be a container image reference '
        'and cannot begin with -: ${value}"'
    ) in task
    assert 'case "${db_result}" in' in task
    assert "*'|'*)" in task
    assert (
        "printf 'Invalid database result; expected active_orders|reserved_quantity, "
        "got: %s\\n' \"${db_result}\" >&2"
    ) in task
    assert (
        "printf 'Concurrency database assertion failed: expected 4|40 with "
        "reserved_quantity <= 42, got %s\\n' \"${db_result}\" >&2"
    ) in task
    assert task.count('active_orders="${db_result%%|*}"') == 1
    assert task.count('reserved_quantity="${db_result#*|}"') == 1
    assert task.count('""|*[!0123456789]*)') >= 2
    assert 'time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())' in task
    assert 'uuid.uuid4().hex[:12], end="")' in task
    assert 'run_id="purchase-concurrency-${run_suffix}"' in task
    assert (
        'if [ "${active_orders}" -ne 4 ] || '
        '[ "${reserved_quantity}" -ne 40 ] || '
        '[ "${reserved_quantity}" -gt 42 ]; then'
    ) in task
