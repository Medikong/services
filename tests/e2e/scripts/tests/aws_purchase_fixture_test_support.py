from __future__ import annotations

import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import NotRequired, TypedDict


REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
VERIFIER = REPOSITORY_ROOT / "tests/e2e/scripts/verify_aws_purchase_fixture.py"
RUN_ID = "aws-purchase-20260723T010203Z-a1b2c3d4"


class SyntheticUser(TypedDict):
    subject_ref: str
    credential_ref: NotRequired[str]
    credential_status: str
    role: str


class FixtureSpec(TypedDict):
    namespace: str
    drop_ref: str
    product_ref: str
    dedicated: bool
    stock: int


class RetentionSpec(TypedDict):
    days: int
    cleanup: str
    automatic_compensation: bool


class FixtureManifest(TypedDict):
    schema_version: int
    run_id: str
    users: list[SyntheticUser]
    fixture: FixtureSpec
    active_records: list[str]
    retention: RetentionSpec


@dataclass(frozen=True, slots=True)
class Invocation:
    result: subprocess.CompletedProcess[str]
    output_path: Path
    state_dir: Path


def _valid_manifest() -> FixtureManifest:
    return {
        "schema_version": 1,
        "run_id": RUN_ID,
        "users": [
            {
                "subject_ref": "opaque-customer-a",
                "credential_ref": "opaque-credential-a",
                "credential_status": "present",
                "role": "customer",
            },
            {
                "subject_ref": "opaque-customer-b",
                "credential_ref": "opaque-credential-b",
                "credential_status": "present",
                "role": "customer",
            },
        ],
        "fixture": {
            "namespace": RUN_ID,
            "drop_ref": "opaque-drop",
            "product_ref": "opaque-product",
            "dedicated": True,
            "stock": 42,
        },
        "active_records": [],
        "retention": {
            "days": 30,
            "cleanup": "retention_only",
            "automatic_compensation": False,
        },
    }


def _invoke_verifier(
    tmp_path: Path,
    manifest_text: str,
    *,
    contract_only: bool = True,
) -> Invocation:
    input_path = tmp_path / "fixture-input.json"
    output_path = tmp_path / "fixture-result.json"
    state_dir = tmp_path / "run-state"
    input_path.write_text(manifest_text, encoding="utf-8")
    command = [
        sys.executable,
        str(VERIFIER),
        "--input",
        str(input_path),
        "--output",
        str(output_path),
        "--state-dir",
        str(state_dir),
    ]
    if contract_only:
        command.append("--contract-only")
    result = subprocess.run(
        command,
        cwd=REPOSITORY_ROOT,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
    )
    return Invocation(result=result, output_path=output_path, state_dir=state_dir)


def _assert_refusal(invocation: Invocation, reason_code: str) -> None:
    assert invocation.result.returncode == 2
    assert invocation.output_path.is_file()
    artifact = json.loads(invocation.output_path.read_text(encoding="utf-8"))
    assert artifact["verdict"] == "REFUSED"
    assert artifact["reason_code"] == reason_code
    assert artifact["api_traffic_allowed"] is False
