import pytest

from app.migrations import main


def test_migration_cli_rejects_downgrade(
    capsys: pytest.CaptureFixture[str],
) -> None:
    exit_code = main(["downgrade"])

    assert exit_code == 2
    assert "do not support downgrade" in capsys.readouterr().err
