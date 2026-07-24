#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///

# ─── How to run ───
# 1. Install uv: https://docs.astral.sh/uv/
# 2. Inspect the required live inputs:
#      uv run tests/e2e/scripts/collect_aws_purchase_live_fixture.py --help
# 3. Run only from an approved read-only AWS-dev control-plane context.
# ──────────────────

from __future__ import annotations

import argparse
import json
import os
import tempfile
from dataclasses import dataclass
from datetime import UTC, datetime
from enum import StrEnum, unique
from pathlib import Path
from typing import Sequence

from aws_purchase_fixture_contract import (
    _JsonObject,
    _JsonValue,
    _Manifest,
    _Refusal,
    _fingerprint,
    _parse_manifest,
)
from aws_purchase_live_fixture_proofs import (
    _LiveProofBlocked,
    _LiveProofConfig,
    verify_live_proofs,
)
from aws_purchase_live_attestation_contract import (
    COLLECTOR_ID,
    attestation_integrity,
    read_attestation_key,
)

@unique
class _Reason(StrEnum):
    INPUT_INVALID = "INPUT_INVALID"
    OUTPUT_STATE_CONFLICT = "OUTPUT_STATE_CONFLICT"
    OUTPUT_WRITE_BLOCKED = "OUTPUT_WRITE_BLOCKED"
    MANIFEST_BLOCKED = "FIXTURE_MANIFEST_BLOCKED"
    ATTESTATION_KEY_BLOCKED = "ATTESTATION_KEY_BLOCKED"


@dataclass(frozen=True, slots=True)
class _Blocked(Exception):
    reason: _Reason

    def __str__(self) -> str:
        return self.reason.value


@dataclass(frozen=True, slots=True)
class _Config:
    environment: str
    fixture_manifest: Path
    live_proof: _LiveProofConfig
    attestation_key_file: Path
    output: Path


def _parse_arguments(arguments: Sequence[str] | None) -> _Config:
    parser = argparse.ArgumentParser(
        description="Collect read-only AWS-dev live fixture proof.",
    )
    parser.add_argument("--version", action="version", version=COLLECTOR_ID)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--fixture-manifest", required=True, type=Path)
    parser.add_argument("--catalog-base-url", required=True)
    parser.add_argument("--kubectl", default="kubectl")
    parser.add_argument("--kube-context", required=True)
    parser.add_argument("--order-namespace", required=True)
    parser.add_argument("--order-db-pod", required=True)
    parser.add_argument("--order-db-container")
    parser.add_argument("--order-db-name", required=True)
    parser.add_argument("--secret-namespace", required=True)
    parser.add_argument("--secret-name", required=True)
    parser.add_argument("--customer-a-email-key", required=True)
    parser.add_argument("--customer-a-password-key", required=True)
    parser.add_argument("--customer-b-email-key", required=True)
    parser.add_argument("--customer-b-password-key", required=True)
    parser.add_argument("--attestation-key-file", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--timeout-seconds", default=10.0, type=float)
    namespace = parser.parse_args(arguments)
    secret_keys = (
        namespace.customer_a_email_key,
        namespace.customer_a_password_key,
        namespace.customer_b_email_key,
        namespace.customer_b_password_key,
    )
    if (
        namespace.environment != "aws-dev"
        or not 0.1 <= namespace.timeout_seconds <= 30
        or any(not key.strip() for key in secret_keys)
        or len(set(secret_keys)) != 4
    ):
        raise _Blocked(_Reason.INPUT_INVALID)
    return _Config(
        environment=namespace.environment,
        fixture_manifest=namespace.fixture_manifest,
        live_proof=_LiveProofConfig(
            catalog_base_url=namespace.catalog_base_url,
            kubectl=namespace.kubectl,
            kube_context=namespace.kube_context,
            order_namespace=namespace.order_namespace,
            order_db_pod=namespace.order_db_pod,
            order_db_container=namespace.order_db_container,
            order_db_name=namespace.order_db_name,
            secret_namespace=namespace.secret_namespace,
            secret_name=namespace.secret_name,
            secret_keys=secret_keys,
            timeout_seconds=namespace.timeout_seconds,
        ),
        attestation_key_file=namespace.attestation_key_file,
        output=namespace.output,
    )


def _load_manifest(path: Path) -> _Manifest:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
        return _parse_manifest(value)
    except (OSError, UnicodeError, json.JSONDecodeError, _Refusal) as error:
        raise _Blocked(_Reason.MANIFEST_BLOCKED) from error


def _artifact(manifest: _Manifest, key: bytes) -> _JsonObject:
    result: _JsonObject = {
        "schema_version": 1,
        "environment": "aws-dev",
        "run_id": manifest.run_id,
        "verdict": "LIVE_FIXTURE_VERIFIED",
        "api_traffic_allowed": True,
        "collector": COLLECTOR_ID,
        "issued_at": datetime.now(UTC)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z"),
        "users": {
            "count": 2,
            "subject_fingerprints": [
                _fingerprint(user.subject_ref) for user in manifest.users
            ],
            "credential_bindings": "VERIFIED",
        },
        "fixture": {
            "drop_fingerprint": _fingerprint(manifest.fixture.drop_ref),
            "product_fingerprint": _fingerprint(manifest.fixture.product_ref),
            "dedicated": True,
            "stock": 42,
            "active_records": 0,
        },
    }
    result["integrity"] = attestation_integrity(result, key)
    return result


def _write_exclusive(path: Path, artifact: _JsonObject) -> str:
    serialized = json.dumps(artifact, separators=(",", ":"), sort_keys=True)
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w",
            encoding="utf-8",
            dir=path.parent,
            prefix=f".{path.name}.",
            suffix=".tmp",
            delete=False,
        ) as temporary:
            temporary_path = Path(temporary.name)
            temporary.write(f"{serialized}\n")
            temporary.flush()
            os.fsync(temporary.fileno())
        os.link(temporary_path, path)
    except OSError as error:
        raise _Blocked(_Reason.OUTPUT_WRITE_BLOCKED) from error
    finally:
        if temporary_path is not None:
            try:
                temporary_path.unlink(missing_ok=True)
            except OSError:
                pass
    return serialized


def _blocked_status(reason: _Reason | str) -> _JsonObject:
    return {
        "schema_version": 1,
        "verdict": "BLOCKED",
        "reason_code": reason.value if type(reason) is _Reason else reason,
        "api_traffic_allowed": False,
    }


def main(arguments: Sequence[str] | None = None) -> int:
    lock_path: Path | None = None
    lock_acquired = False
    try:
        config = _parse_arguments(arguments)
        config.output.parent.mkdir(parents=True, exist_ok=True)
        lock_path = config.output.with_suffix(config.output.suffix + ".lock")
        if config.output.exists():
            raise _Blocked(_Reason.OUTPUT_STATE_CONFLICT)
        descriptor = os.open(
            lock_path,
            os.O_CREAT | os.O_EXCL | os.O_WRONLY,
            0o600,
        )
        lock_acquired = True
        os.close(descriptor)
        if config.output.exists():
            raise _Blocked(_Reason.OUTPUT_STATE_CONFLICT)
        manifest = _load_manifest(config.fixture_manifest)
        key = read_attestation_key(config.attestation_key_file)
        if key is None:
            raise _Blocked(_Reason.ATTESTATION_KEY_BLOCKED)
        verify_live_proofs(config.live_proof, manifest)
        print(_write_exclusive(config.output, _artifact(manifest, key)))
    except FileExistsError:
        print(
            json.dumps(
                _blocked_status(_Reason.OUTPUT_STATE_CONFLICT),
                separators=(",", ":"),
                sort_keys=True,
            )
        )
        return 3
    except OSError:
        print(
            json.dumps(
                _blocked_status(_Reason.OUTPUT_WRITE_BLOCKED),
                separators=(",", ":"),
                sort_keys=True,
            )
        )
        return 4
    except _Blocked as blocked:
        print(
            json.dumps(
                _blocked_status(blocked.reason),
                separators=(",", ":"),
                sort_keys=True,
            )
        )
        return 4 if blocked.reason is _Reason.OUTPUT_WRITE_BLOCKED else 3
    except _LiveProofBlocked as blocked:
        print(
            json.dumps(
                _blocked_status(blocked.reason.value),
                separators=(",", ":"),
                sort_keys=True,
            )
        )
        return 3
    finally:
        if lock_acquired and lock_path is not None:
            try:
                lock_path.unlink(missing_ok=True)
            except OSError:
                pass
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
