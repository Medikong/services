#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = [
#     "httpx2[http2,brotli,zstd]>=2.7,<3",
#     "pydantic>=2.12,<3",
#     "pyyaml>=6,<7",
# ]
# ///
# noqa: SIZE_OK - one CLI boundary owns parsing, execution, and redacted output.

# ─── How to run ───
# 1. Install uv: https://docs.astral.sh/uv/
# 2. Inspect options:
#      uv run tests/e2e/scripts/run_aws_purchase_scenarios.py --help
# 3. Credentials are accepted only through the documented environment variables.
# ──────────────────

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from types import TracebackType
from typing import assert_never

from aws_purchase_scenario_04 import execute_happy_path
from aws_purchase_scenario_config import RawInputs, build_config
from aws_purchase_scenario_contracts import contract_prerequisites
from aws_purchase_scenario_http import ScenarioHttpClient
from aws_purchase_scenario_models import (
    Bounds,
    Config,
    JsonObject,
    Mode,
    Report,
    RunnerStop,
    Scenario,
    ScenarioSummary,
    Verdict,
)

_EXIT_CODES = {
    Verdict.READY: 0,
    Verdict.PASS: 0,
    Verdict.FAIL: 2,
    Verdict.BLOCKED: 3,
}


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Run bounded AWS-dev purchase scenarios through Istio."
    )
    parser.add_argument("--environment")
    parser.add_argument("--mode", required=True)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--base-url")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--fixture-manifest", required=True, type=Path)
    parser.add_argument("--live-fixture-attestation", type=Path)
    parser.add_argument("--attestation-key-file", type=Path)
    parser.add_argument("--write-opt-in")
    parser.add_argument("--json-output", required=True, type=Path)
    parser.add_argument("--max-attempts", default=2, type=int)
    parser.add_argument("--poll-attempts", default=10, type=int)
    parser.add_argument("--poll-interval-seconds", default=0.5, type=float)
    parser.add_argument("--timeout-seconds", default=5.0, type=float)
    return parser


def _raw_inputs(namespace: argparse.Namespace) -> RawInputs:
    return RawInputs(
        environment=namespace.environment,
        mode=namespace.mode,
        scenario=namespace.scenario,
        base_url=namespace.base_url,
        run_id=namespace.run_id,
        output_path=namespace.json_output,
        fixture_manifest=namespace.fixture_manifest,
        live_fixture_attestation=namespace.live_fixture_attestation,
        attestation_key_file=namespace.attestation_key_file,
        write_opt_in=namespace.write_opt_in,
        bounds=Bounds(
            max_attempts=namespace.max_attempts,
            poll_attempts=namespace.poll_attempts,
            poll_interval_seconds=namespace.poll_interval_seconds,
            timeout_seconds=namespace.timeout_seconds,
        ),
    )


def _report(
    *,
    config: Config,
    verdict: Verdict,
    reason_code: str,
    client: ScenarioHttpClient | None = None,
    prerequisites: tuple[str, ...] = (),
    summary: ScenarioSummary | None = None,
) -> Report:
    return Report(
        run_id=config.run_id,
        environment=config.environment,
        mode=config.mode.value,
        scenario=config.scenario.value,
        verdict=verdict,
        reason_code=reason_code,
        ingress_fingerprint=config.ingress_fingerprint,
        fixture=config.fixture,
        requests_sent=client.requests_sent if client is not None else 0,
        purchase_write_requests_sent=(
            client.purchase_writes_sent if client is not None else 0
        ),
        stages=client.stages if client is not None else (),
        prerequisites=prerequisites,
        summary=summary,
    )


def _blocked_without_config(raw: RawInputs, stop: RunnerStop) -> Report:
    run_id = raw.run_id if raw.run_id.startswith("aws-purchase-") else "invalid"
    return Report(
        run_id=run_id,
        environment=(raw.environment or "unconfigured"),
        mode=raw.mode,
        scenario=raw.scenario,
        verdict=stop.verdict,
        reason_code=stop.reason_code,
        ingress_fingerprint="unconfigured",
        fixture=None,
        requests_sent=0,
        purchase_write_requests_sent=0,
        stages=(),
        prerequisites=stop.prerequisites,
    )


def _run(raw: RawInputs) -> Report:
    try:
        config = build_config(raw, os.environ)
    except RunnerStop as stop:
        return _blocked_without_config(raw, stop)
    try:
        prerequisites = contract_prerequisites(
            Path(__file__).resolve().parents[3],
            config.scenario,
        )
        if prerequisites:
            return _report(
                config=config,
                verdict=Verdict.BLOCKED,
                reason_code="API_CONTRACT_UNSUPPORTED",
                prerequisites=prerequisites,
            )
        match config.mode:
            case Mode.DRY_RUN:
                return _report(
                    config=config,
                    verdict=Verdict.READY,
                    reason_code="DRY_RUN_VERIFIED",
                )
            case Mode.PREFLIGHT | Mode.EXECUTE:
                pass
            case unreachable:
                assert_never(unreachable)
        with ScenarioHttpClient(config) as client:
            try:
                token = client.authenticate(config.credentials)
                catalog = client.preflight(token, config.fixture)
                if config.mode is Mode.PREFLIGHT:
                    return _report(
                        config=config,
                        verdict=Verdict.READY,
                        reason_code="PREFLIGHT_VERIFIED",
                        client=client,
                    )
                match config.scenario:
                    case Scenario.HAPPY_PATH:
                        summary = execute_happy_path(
                            client,
                            config,
                            token,
                            catalog,
                        )
                    case (
                        Scenario.PAYMENT_FAILURE
                        | Scenario.LOW_STOCK_CONCURRENCY
                    ):
                        raise RunnerStop(
                            Verdict.BLOCKED,
                            "API_CONTRACT_UNSUPPORTED",
                        )
                    case unreachable:
                        assert_never(unreachable)
                return _report(
                    config=config,
                    verdict=Verdict.PASS,
                    reason_code="SCENARIO_04_PASSED",
                    client=client,
                    summary=summary,
                )
            except RunnerStop as stop:
                return _report(
                    config=config,
                    verdict=stop.verdict,
                    reason_code=stop.reason_code,
                    client=client,
                    prerequisites=stop.prerequisites,
                )
    except RunnerStop as stop:
        return _report(
            config=config,
            verdict=stop.verdict,
            reason_code=stop.reason_code,
            prerequisites=stop.prerequisites,
        )


def _write_evidence(path: Path, report: Report) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(report.as_json(), stream, indent=2, sort_keys=True)
        stream.write("\n")


def _emit(value: JsonObject) -> None:
    print(json.dumps(value, separators=(",", ":"), sort_keys=True))


def main() -> int:
    namespace = _parser().parse_args()
    raw = _raw_inputs(namespace)
    if raw.output_path.exists():
        _emit(
            {
                "schema_version": 1,
                "verdict": Verdict.BLOCKED.value,
                "reason_code": "OUTPUT_STATE_CONFLICT",
                "purchase_write_requests_sent": 0,
            }
        )
        return _EXIT_CODES[Verdict.BLOCKED]
    try:
        report = _run(raw)
        _write_evidence(raw.output_path, report)
    except FileExistsError:
        _emit(
            {
                "schema_version": 1,
                "verdict": Verdict.BLOCKED.value,
                "reason_code": "OUTPUT_STATE_CONFLICT",
                "purchase_write_requests_sent": 0,
            }
        )
        return _EXIT_CODES[Verdict.BLOCKED]
    except OSError:
        _emit(
            {
                "schema_version": 1,
                "verdict": Verdict.FAIL.value,
                "reason_code": "OUTPUT_WRITE_FAILED",
                "purchase_write_requests_sent": 0,
            }
        )
        return 4
    _emit(report.as_json())
    return _EXIT_CODES[report.verdict]


def _redacted_excepthook(
    exception_type: type[BaseException],
    exception: BaseException,
    traceback: TracebackType | None,
) -> None:
    del exception_type, exception, traceback
    _emit(
        {
            "schema_version": 1,
            "verdict": Verdict.BLOCKED.value,
            "reason_code": "INTERNAL_ERROR",
            "purchase_write_requests_sent": 0,
        }
    )


if __name__ == "__main__":
    sys.excepthook = _redacted_excepthook
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        _emit(
            {
                "schema_version": 1,
                "verdict": Verdict.BLOCKED.value,
                "reason_code": "INTERRUPTED",
                "purchase_write_requests_sent": 0,
            }
        )
        raise SystemExit(130) from None
