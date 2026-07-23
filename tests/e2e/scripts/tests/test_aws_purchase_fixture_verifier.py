from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

import pytest

from aws_purchase_fixture_test_support import (
    REPOSITORY_ROOT,
    RUN_ID,
    VERIFIER,
    _assert_refusal,
    _invoke_verifier,
    _valid_manifest,
)


def test_local_contract_emits_only_redacted_run_manifest(tmp_path: Path) -> None:
    manifest = _valid_manifest()

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    assert invocation.result.returncode == 0
    artifact = json.loads(invocation.output_path.read_text(encoding="utf-8"))
    serialized = invocation.output_path.read_text(encoding="utf-8")
    assert artifact["verdict"] == "LOCAL_CONTRACT_VERIFIED"
    assert artifact["api_traffic_allowed"] is False
    assert artifact["run_id"] == RUN_ID
    assert artifact["users"]["count"] == 2
    assert artifact["fixture"]["stock"] == 42
    assert artifact["runtime_provisioning"]["status"] == "BLOCKED"
    assert "opaque-customer-a" not in serialized
    assert "opaque-credential-a" not in serialized
    assert "opaque-drop" not in serialized
    assert not invocation.output_path.with_suffix(".json.lock").exists()


def test_refuses_missing_second_user(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["users"].pop()

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "USER_COUNT_INVALID")


def test_refuses_missing_credentials(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["users"][1].pop("credential_ref")

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "CREDENTIALS_MISSING")


def test_refuses_duplicate_users(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["users"][1]["subject_ref"] = manifest["users"][0]["subject_ref"]

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "DUPLICATE_USERS")


def test_refuses_non_42_stock(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["fixture"]["stock"] = 41

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "FIXTURE_STOCK_INVALID")


def test_refuses_fixture_outside_run_namespace(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["fixture"]["namespace"] = "shared-fixture"

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "FIXTURE_SCOPE_INVALID")


def test_refuses_pre_existing_active_records(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["active_records"] = ["opaque-active-record"]

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "ACTIVE_RECORDS_PRESENT")


def test_refuses_shared_reset_retention_policy(tmp_path: Path) -> None:
    manifest = _valid_manifest()
    manifest["retention"]["cleanup"] = "database_reset"

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "RETENTION_POLICY_INVALID")


def test_refuses_reused_run_id(tmp_path: Path) -> None:
    first = _invoke_verifier(tmp_path, json.dumps(_valid_manifest()))
    first.output_path.rename(tmp_path / "first-result.json")

    second = _invoke_verifier(tmp_path, json.dumps(_valid_manifest()))

    assert first.result.returncode == 0
    _assert_refusal(second, "RUN_ID_REUSED")


@pytest.mark.parametrize(
    ("manifest_text", "reason_code"),
    [
        ("{", "MANIFEST_INVALID"),
        ("[]", "MANIFEST_INVALID"),
    ],
)
def test_refuses_malformed_manifest(
    tmp_path: Path,
    manifest_text: str,
    reason_code: str,
) -> None:
    invocation = _invoke_verifier(tmp_path, manifest_text)

    _assert_refusal(invocation, reason_code)


def test_refuses_missing_manifest_with_classified_artifact(tmp_path: Path) -> None:
    output_path = tmp_path / "fixture-result.json"
    result = subprocess.run(
        [
            sys.executable,
            str(VERIFIER),
            "--input",
            str(tmp_path / "missing.json"),
            "--output",
            str(output_path),
            "--state-dir",
            str(tmp_path / "run-state"),
            "--contract-only",
        ],
        cwd=REPOSITORY_ROOT,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )

    assert result.returncode == 2
    artifact = json.loads(output_path.read_text(encoding="utf-8"))
    assert artifact["reason_code"] == "MANIFEST_MISSING"
    assert artifact["api_traffic_allowed"] is False


def test_default_preflight_blocks_without_live_provisioning_source(
    tmp_path: Path,
) -> None:
    invocation = _invoke_verifier(
        tmp_path,
        json.dumps(_valid_manifest()),
        contract_only=False,
    )

    assert invocation.result.returncode == 3
    artifact = json.loads(invocation.output_path.read_text(encoding="utf-8"))
    assert artifact["verdict"] == "BLOCKED"
    assert artifact["reason_code"] == "LIVE_PROVISIONING_UNAVAILABLE"
    assert artifact["api_traffic_allowed"] is False


def test_blocked_preflight_still_claims_immutable_run_id(tmp_path: Path) -> None:
    first = _invoke_verifier(
        tmp_path,
        json.dumps(_valid_manifest()),
        contract_only=False,
    )
    first.output_path.rename(tmp_path / "first-result.json")

    second = _invoke_verifier(tmp_path, json.dumps(_valid_manifest()))

    assert first.result.returncode == 3
    _assert_refusal(second, "RUN_ID_REUSED")


def test_redacts_secrets_and_pii_from_every_output_channel(tmp_path: Path) -> None:
    sensitive_values = (
        "sensitive-value-01",
        "sensitive-value-02",
        "sensitive-value-03",
        "sensitive-value-04",
        "sensitive-value-05",
    )
    manifest = {
        **_valid_manifest(),
        "email": sensitive_values[0],
        "password": sensitive_values[1],
        "token": sensitive_values[2],
        "cookie": sensitive_values[3],
        "authorization": sensitive_values[4],
    }

    invocation = _invoke_verifier(tmp_path, json.dumps(manifest))

    _assert_refusal(invocation, "MANIFEST_INVALID")
    emitted = "\n".join(
        (
            invocation.result.stdout,
            invocation.result.stderr,
            invocation.output_path.read_text(encoding="utf-8"),
        )
    )
    for sensitive_value in sensitive_values:
        assert sensitive_value not in emitted


def test_interrupted_output_lock_never_leaves_partial_manifest(
    tmp_path: Path,
) -> None:
    output_path = tmp_path / "fixture-result.json"
    output_path.with_suffix(".json.lock").write_text("", encoding="ascii")

    invocation = _invoke_verifier(tmp_path, json.dumps(_valid_manifest()))

    assert invocation.result.returncode == 4
    assert not output_path.exists()
    assert output_path.with_suffix(".json.lock").exists()
    status = json.loads(invocation.result.stdout)
    assert status["verdict"] == "BLOCKED"
    assert status["reason_code"] == "ARTIFACT_WRITE_BLOCKED"
