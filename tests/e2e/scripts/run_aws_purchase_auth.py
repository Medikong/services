#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["httpx2[http2,brotli,zstd]>=2.7,<3"]
# ///

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import assert_never
from xml.etree import ElementTree

from aws_purchase_auth_contract import (
    ConfigurationStop,
    Report,
    RunnerConfig,
    StageRecord,
    TokenCredential,
    Verdict,
    build_config,
    load_credential,
)
from aws_purchase_auth_http import AuthProbe


_EXIT_CODES = {
    Verdict.VERIFIED: 0,
    Verdict.FAIL: 2,
    Verdict.BLOCKED: 3,
}


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Verify the public AWS Istio JWT boundary without purchase traffic."
    )
    parser.add_argument("--base-url")
    parser.add_argument("--auth-route", default="/.well-known/jwks.json")
    parser.add_argument("--protected-route", default="/v1/users/me/interests")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--json-output", required=True, type=Path)
    parser.add_argument("--junit-output", required=True, type=Path)
    parser.add_argument("--max-attempts", default=3, type=int)
    parser.add_argument("--backoff-seconds", default=0.25, type=float)
    parser.add_argument("--timeout-seconds", default=5.0, type=float)
    return parser


def _auth_mode(environment: dict[str, str]) -> str:
    if environment.get("AWS_PURCHASE_JWT", "").strip():
        return "token"
    if (
        environment.get("SYNTHETIC_CUSTOMER_EMAIL", "").strip()
        or environment.get("SYNTHETIC_CUSTOMER_PASSWORD", "").strip()
    ):
        return "login"
    return "none"


def _report(
    *,
    run_id: str,
    verdict: Verdict,
    reason_code: str,
    auth_mode: str,
    fingerprint: str,
    stages: tuple[StageRecord, ...] = (),
) -> Report:
    return Report(
        run_id=run_id,
        verdict=verdict,
        reason_code=reason_code,
        auth_mode=auth_mode,
        ingress_fingerprint=fingerprint,
        stages=stages,
    )


def _junit_xml(report: Report) -> str:
    failures = "1" if report.verdict is Verdict.FAIL else "0"
    skipped = "1" if report.verdict is Verdict.BLOCKED else "0"
    suite = ElementTree.Element(
        "testsuite",
        {
            "name": "aws-istio-jwt-auth-preflight",
            "tests": "1",
            "failures": failures,
            "errors": "0",
            "skipped": skipped,
        },
    )
    case = ElementTree.SubElement(
        suite,
        "testcase",
        {
            "classname": "e2e.aws_purchase",
            "name": "public-jwt-boundary",
        },
    )
    match report.verdict:
        case Verdict.FAIL:
            ElementTree.SubElement(
                case,
                "failure",
                {"type": report.reason_code, "message": report.reason_code},
            )
        case Verdict.BLOCKED:
            ElementTree.SubElement(
                case,
                "skipped",
                {"type": report.reason_code, "message": report.reason_code},
            )
        case Verdict.VERIFIED:
            pass
        case unreachable:
            assert_never(unreachable)
    ElementTree.indent(suite)
    return ElementTree.tostring(suite, encoding="unicode")


def _emit_stdout(report: Report) -> None:
    print(json.dumps(report.as_json(), separators=(",", ":"), sort_keys=True))


def _write_outputs(
    json_output: Path,
    junit_output: Path,
    report: Report,
) -> None:
    for output in (json_output, junit_output):
        output.parent.mkdir(parents=True, exist_ok=True)
    with json_output.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(report.as_json(), stream, indent=2, sort_keys=True)
        stream.write("\n")
    with junit_output.open("x", encoding="utf-8", newline="\n") as stream:
        stream.write(_junit_xml(report))
        stream.write("\n")


def _state_conflict(auth_mode: str) -> int:
    report = _report(
        run_id="unvalidated",
        verdict=Verdict.BLOCKED,
        reason_code="OUTPUT_STATE_CONFLICT",
        auth_mode=auth_mode,
        fingerprint="unconfigured",
    )
    _emit_stdout(report)
    return _EXIT_CODES[Verdict.BLOCKED]


def main() -> int:
    args = _parser().parse_args()
    environment = dict(os.environ)
    auth_mode = _auth_mode(environment)
    if args.json_output.exists() or args.junit_output.exists():
        return _state_conflict(auth_mode)
    try:
        config = build_config(
            base_url_argument=args.base_url,
            base_url_environment=environment.get("AWS_PURCHASE_INGRESS_BASE_URL"),
            auth_route=args.auth_route,
            protected_route=args.protected_route,
            run_id=args.run_id,
            json_output=args.json_output,
            junit_output=args.junit_output,
            max_attempts=args.max_attempts,
            backoff_seconds=args.backoff_seconds,
            timeout_seconds=args.timeout_seconds,
            expected_ingress_fingerprint=environment.get(
                "AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT"
            ),
        )
    except ConfigurationStop as stop:
        run_id = "invalid" if stop.reason_code == "RUN_ID_INVALID" else args.run_id
        report = _report(
            run_id=run_id,
            verdict=Verdict.BLOCKED,
            reason_code=stop.reason_code,
            auth_mode=auth_mode,
            fingerprint=stop.fingerprint,
        )
        return _persist_without_config(args, report)
    try:
        credential = load_credential(
            token=environment.get("AWS_PURCHASE_JWT"),
            email=environment.get("SYNTHETIC_CUSTOMER_EMAIL"),
            password=environment.get("SYNTHETIC_CUSTOMER_PASSWORD"),
        )
    except ConfigurationStop as stop:
        report = _report(
            run_id=config.run_id,
            verdict=Verdict.BLOCKED,
            reason_code=stop.reason_code,
            auth_mode=auth_mode,
            fingerprint=config.ingress_fingerprint,
        )
        return _persist(config, report)
    auth_mode = "token" if isinstance(credential, TokenCredential) else "login"
    try:
        result = AuthProbe(config).run(credential)
        report = _report(
            run_id=config.run_id,
            verdict=result.verdict,
            reason_code=result.reason_code,
            auth_mode=auth_mode,
            fingerprint=config.ingress_fingerprint,
            stages=result.stages,
        )
    except KeyboardInterrupt:
        report = _report(
            run_id=config.run_id,
            verdict=Verdict.BLOCKED,
            reason_code="INTERRUPTED",
            auth_mode=auth_mode,
            fingerprint=config.ingress_fingerprint,
        )
        _persist(config, report)
        return 130
    return _persist(config, report)


def _persist(config: RunnerConfig, report: Report) -> int:
    try:
        _write_outputs(config.json_output, config.junit_output, report)
    except FileExistsError:
        return _state_conflict(report.auth_mode)
    except OSError:
        failure = _report(
            run_id=report.run_id,
            verdict=Verdict.FAIL,
            reason_code="OUTPUT_WRITE_FAILED",
            auth_mode=report.auth_mode,
            fingerprint=report.ingress_fingerprint,
        )
        _emit_stdout(failure)
        return 4
    _emit_stdout(report)
    return _EXIT_CODES[report.verdict]


def _persist_without_config(
    args: argparse.Namespace,
    report: Report,
) -> int:
    try:
        _write_outputs(args.json_output, args.junit_output, report)
    except FileExistsError:
        return _state_conflict(report.auth_mode)
    except OSError:
        _emit_stdout(
            _report(
                run_id=report.run_id,
                verdict=Verdict.FAIL,
                reason_code="OUTPUT_WRITE_FAILED",
                auth_mode=report.auth_mode,
                fingerprint=report.ingress_fingerprint,
            )
        )
        return 4
    _emit_stdout(report)
    return _EXIT_CODES[report.verdict]


if __name__ == "__main__":
    sys.exit(main())
