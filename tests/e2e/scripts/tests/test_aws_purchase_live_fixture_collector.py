from __future__ import annotations

import hashlib
import hmac
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import collect_aws_purchase_live_fixture as collector
from aws_purchase_live_fixture_collector_test_support import (
    ATTESTATION_KEY,
    FakeProofs,
    catalog_payload,
    collector_arguments,
    install_fakes,
)
from aws_purchase_fixture_test_support import RUN_ID


def test_emits_exact_signed_contract_after_all_read_only_proofs(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    requests, commands = install_fakes(
        monkeypatch,
        FakeProofs(catalog_payload=catalog_payload()),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 0
    artifact = json.loads(output_path.read_text(encoding="utf-8"))
    assert set(artifact) == {
        "api_traffic_allowed",
        "collector",
        "environment",
        "fixture",
        "integrity",
        "issued_at",
        "run_id",
        "schema_version",
        "users",
        "verdict",
    }
    assert artifact["verdict"] == "LIVE_FIXTURE_VERIFIED"
    assert artifact["api_traffic_allowed"] is True
    assert artifact["run_id"] == RUN_ID
    unsigned = dict(artifact)
    integrity = unsigned.pop("integrity")
    expected_integrity = "hmac-sha256:" + hmac.new(
        ATTESTATION_KEY,
        json.dumps(
            unsigned,
            separators=(",", ":"),
            sort_keys=True,
        ).encode(),
        hashlib.sha256,
    ).hexdigest()
    assert integrity == expected_integrity
    assert len(requests) == 1
    assert requests[0].method == "GET"
    assert requests[0].data is None
    assert requests[0].get_header("Authorization") is None
    assert len(commands) == 2
    order_command = " ".join(commands[0])
    assert "exec" in commands[0]
    assert "psql" in commands[0]
    assert "SELECT" in order_command
    assert not {"INSERT", "UPDATE", "DELETE"} & set(order_command.split())
    secret_command = " ".join(commands[1])
    assert "go-template" in secret_command
    assert "base64" not in secret_command
    emitted = "\n".join(
        (
            capsys.readouterr().out,
            output_path.read_text(encoding="utf-8"),
        )
    )
    for sensitive in (
        ATTESTATION_KEY.decode(),
        "opaque-customer-a",
        "opaque-credential-a",
        "opaque-drop",
        "opaque-product",
        "Authorization",
    ):
        assert sensitive not in emitted
    assert not output_path.with_suffix(".json.lock").exists()


@pytest.mark.parametrize(
    "catalog_payload",
    [
        catalog_payload(status="CLOSED"),
        catalog_payload(stock=41),
    ],
)
def test_invalid_catalog_proof_blocks_without_output(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    catalog_payload: bytes,
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    _, commands = install_fakes(
        monkeypatch,
        FakeProofs(catalog_payload=catalog_payload),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 3
    assert not output_path.exists()
    assert commands == []


@pytest.mark.parametrize(
    "order_output",
    ["41|0|0|0\n", "42|1|0|0\n", "42|0|1|0\n", "42|0|0|1\n"],
)
def test_invalid_order_proof_blocks_without_output(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    order_output: str,
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    _, commands = install_fakes(
        monkeypatch,
        FakeProofs(
            catalog_payload=catalog_payload(),
            order_output=order_output,
        ),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 3
    assert not output_path.exists()
    assert len(commands) == 1


def test_invalid_secret_proof_blocks_and_redacts_subprocess_output(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    sensitive = "Authorization: Bearer secret-token body=credential-value"
    install_fakes(
        monkeypatch,
        FakeProofs(
            catalog_payload=catalog_payload(),
            secret_output="credential-value",
            secret_return_code=1,
            secret_error=sensitive,
        ),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 3
    assert not output_path.exists()
    emitted = capsys.readouterr()
    assert sensitive not in emitted.out
    assert sensitive not in emitted.err
    assert "SECRET_PROOF_BLOCKED" in emitted.out


def test_existing_output_blocks_before_network_or_kubectl(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    output_path.write_text("stale", encoding="utf-8")
    requests, commands = install_fakes(
        monkeypatch,
        FakeProofs(catalog_payload=catalog_payload()),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 3
    assert output_path.read_text(encoding="utf-8") == "stale"
    assert requests == []
    assert commands == []


def test_existing_lock_remains_owned_by_other_collector(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # Given
    arguments, output_path = collector_arguments(tmp_path)
    lock_path = output_path.with_suffix(".json.lock")
    lock_path.write_text("owned", encoding="ascii")
    requests, commands = install_fakes(
        monkeypatch,
        FakeProofs(catalog_payload=catalog_payload()),
    )

    # When
    exit_code = collector.main(arguments)

    # Then
    assert exit_code == 3
    assert not output_path.exists()
    assert lock_path.read_text(encoding="ascii") == "owned"
    assert requests == []
    assert commands == []
