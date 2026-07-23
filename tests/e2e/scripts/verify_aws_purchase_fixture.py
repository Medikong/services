#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///
#
# How to run:
# 1. Install uv: https://docs.astral.sh/uv/
# 2. Run: uv run tests/e2e/scripts/verify_aws_purchase_fixture.py --help
# 3. On Unix, make executable and run the script directly.

from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from types import TracebackType
from typing import Never

from aws_purchase_fixture_contract import (
    _JsonObject,
    _Manifest,
    _OperationalBlock,
    _ReasonCode,
    _Refusal,
    _artifact,
    _fingerprint,
    _parse_manifest,
)


@dataclass(frozen=True, slots=True)
class _Arguments:
    input_path: Path
    output_path: Path
    state_dir: Path
    contract_only: bool


class _SafeArgumentParser(argparse.ArgumentParser):
    def error(self, message: str) -> Never:
        del message
        print(json.dumps(_artifact("REFUSED", _ReasonCode.MANIFEST_INVALID)))
        raise SystemExit(2)


def _parse_arguments() -> _Arguments:
    parser = _SafeArgumentParser(
        description="Validate a redacted AWS purchase fixture manifest.",
    )
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--state-dir", required=True, type=Path)
    parser.add_argument(
        "--contract-only",
        action="store_true",
        help="Validate an offline manifest without permitting API traffic.",
    )
    namespace = parser.parse_args()
    return _Arguments(
        input_path=namespace.input,
        output_path=namespace.output,
        state_dir=namespace.state_dir,
        contract_only=namespace.contract_only,
    )


def _load_manifest(path: Path) -> _Manifest:
    try:
        raw = path.read_text(encoding="utf-8")
    except FileNotFoundError as error:
        raise _Refusal(_ReasonCode.MANIFEST_MISSING) from error
    except (OSError, UnicodeError) as error:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID) from error
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as error:
        raise _Refusal(_ReasonCode.MANIFEST_INVALID) from error
    return _parse_manifest(value)


def _claim_run(state_dir: Path, run_id: str) -> None:
    try:
        state_dir.mkdir(parents=True, exist_ok=True)
    except OSError as error:
        raise _OperationalBlock(_ReasonCode.STATE_WRITE_BLOCKED) from error
    claim_path = state_dir / f"{_fingerprint(run_id)}.claim"
    try:
        descriptor = os.open(
            claim_path,
            os.O_CREAT | os.O_EXCL | os.O_WRONLY,
            0o600,
        )
    except FileExistsError as error:
        raise _Refusal(_ReasonCode.RUN_ID_REUSED) from error
    except OSError as error:
        raise _OperationalBlock(_ReasonCode.STATE_WRITE_BLOCKED) from error
    with os.fdopen(descriptor, "w", encoding="ascii") as claim:
        claim.write("claimed\n")
        claim.flush()
        os.fsync(claim.fileno())


def _write_artifact(path: Path, artifact: _JsonObject) -> str:
    serialized = json.dumps(artifact, sort_keys=True, separators=(",", ":"))
    lock_path = path.with_suffix(path.suffix + ".lock")
    lock_acquired = False
    temporary_path: Path | None = None
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        lock_descriptor = os.open(
            lock_path,
            os.O_CREAT | os.O_EXCL | os.O_WRONLY,
            0o600,
        )
        os.close(lock_descriptor)
        lock_acquired = True
        if path.exists():
            raise FileExistsError
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
        os.replace(temporary_path, path)
        temporary_path = None
    except OSError as error:
        raise _OperationalBlock(_ReasonCode.ARTIFACT_WRITE_BLOCKED) from error
    finally:
        if temporary_path is not None:
            _unlink_owned(temporary_path)
        if lock_acquired:
            _unlink_owned(lock_path)
    return serialized


def _unlink_owned(path: Path) -> None:
    try:
        path.unlink(missing_ok=True)
    except OSError as error:
        raise _OperationalBlock(
            _ReasonCode.ARTIFACT_WRITE_BLOCKED,
        ) from error


def _persist_and_print(
    output_path: Path,
    artifact: _JsonObject,
    exit_code: int,
) -> int:
    try:
        serialized = _write_artifact(output_path, artifact)
    except _OperationalBlock:
        fallback = _artifact("BLOCKED", _ReasonCode.ARTIFACT_WRITE_BLOCKED)
        print(json.dumps(fallback, sort_keys=True, separators=(",", ":")))
        return 4
    print(serialized)
    return exit_code


def _main() -> int:
    arguments = _parse_arguments()
    manifest: _Manifest | None = None
    try:
        manifest = _load_manifest(arguments.input_path)
        _claim_run(arguments.state_dir, manifest.run_id)
        if not arguments.contract_only:
            return _persist_and_print(
                arguments.output_path,
                _artifact(
                    "BLOCKED",
                    _ReasonCode.LIVE_PROVISIONING_UNAVAILABLE,
                    manifest,
                ),
                3,
            )
    except _Refusal as refusal:
        return _persist_and_print(
            arguments.output_path,
            _artifact("REFUSED", refusal.reason, manifest),
            2,
        )
    except _OperationalBlock as block:
        return _persist_and_print(
            arguments.output_path,
            _artifact("BLOCKED", block.reason, manifest),
            4,
        )
    return _persist_and_print(
        arguments.output_path,
        _artifact("LOCAL_CONTRACT_VERIFIED", _ReasonCode.NONE, manifest),
        0,
    )


def _redacted_excepthook(
    exception_type: type[BaseException],
    exception: BaseException,
    traceback: TracebackType | None,
) -> None:
    del exception_type, exception, traceback
    status = _artifact("BLOCKED", _ReasonCode.INTERNAL_ERROR)
    print(
        json.dumps(status, sort_keys=True, separators=(",", ":")),
        file=sys.stderr,
    )


if __name__ == "__main__":
    sys.excepthook = _redacted_excepthook
    try:
        raise SystemExit(_main())
    except KeyboardInterrupt:
        print(
            json.dumps(
                _artifact("BLOCKED", _ReasonCode.INTERRUPTED),
                sort_keys=True,
                separators=(",", ":"),
            ),
            file=sys.stderr,
        )
        raise SystemExit(130) from None
