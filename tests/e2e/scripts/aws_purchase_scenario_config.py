# noqa: SIZE_OK - configuration parsing and fail-closed gates share one boundary.
from __future__ import annotations

import hmac
import ipaddress
import json
import re
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Final, Mapping
from urllib.parse import urlsplit, urlunsplit

from pydantic import TypeAdapter, ValidationError

from aws_purchase_fixture_contract import _Refusal, _parse_manifest
from aws_purchase_live_attestation_contract import (
    COLLECTOR_ID,
    MAX_ATTESTATION_AGE_SECONDS,
    MAX_ATTESTATION_FUTURE_SKEW_SECONDS,
    integrity_matches,
    read_attestation_key,
)
from aws_purchase_scenario_models import (
    Bounds,
    Config,
    Credentials,
    Fixture,
    FixtureId,
    JsonObject,
    JsonValue,
    Mode,
    RunnerStop,
    Scenario,
    Verdict,
    fingerprint,
)

_RUN_ID_PATTERN: Final = re.compile(
    r"aws-purchase-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}"
)
_FINGERPRINT_PATTERN: Final = re.compile(r"sha256:[a-f0-9]{64}")
_DIRECT_SERVICE_LABELS: Final = frozenset(
    {
        "auth-service",
        "catalog-service",
        "notification-service",
        "order-service",
        "payment-service",
    }
)
_JSON_ADAPTER: Final = TypeAdapter(JsonValue)


@dataclass(frozen=True, slots=True)
class RawInputs:
    environment: str | None
    mode: str
    scenario: str
    base_url: str | None
    run_id: str
    output_path: Path
    fixture_manifest: Path
    live_fixture_attestation: Path | None
    attestation_key_file: Path | None
    write_opt_in: str | None
    bounds: Bounds


def build_config(raw: RawInputs, environment: Mapping[str, str]) -> Config:
    selected_environment = (raw.environment or "").strip()
    if not selected_environment:
        raise RunnerStop(Verdict.BLOCKED, "ENVIRONMENT_REQUIRED")
    if selected_environment != "aws-dev":
        raise RunnerStop(Verdict.BLOCKED, "ENVIRONMENT_NOT_ALLOWED")
    mode = _parse_mode(raw.mode)
    scenario = _parse_scenario(raw.scenario)
    if _RUN_ID_PATTERN.fullmatch(raw.run_id) is None:
        raise RunnerStop(Verdict.BLOCKED, "RUN_ID_INVALID")
    base_url = _select_base_url(
        raw.base_url,
        environment.get("AWS_PURCHASE_INGRESS_BASE_URL"),
    )
    normalized_url = _normalize_base_url(base_url)
    ingress_fingerprint = f"sha256:{_sha256(normalized_url)}"
    _verify_ingress_fingerprint(
        environment.get("AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT"),
        ingress_fingerprint,
    )
    _verify_bounds(raw.bounds)
    fixture = _load_fixture(raw.fixture_manifest, raw.run_id)
    credentials = _load_credentials(environment)
    second_credentials = _load_second_credentials(environment)
    live_fixture_verified = False
    if mode is Mode.EXECUTE:
        _verify_write_opt_in(raw.write_opt_in, raw.run_id)
        if raw.live_fixture_attestation is None:
            raise RunnerStop(
                Verdict.BLOCKED,
                "LIVE_FIXTURE_ATTESTATION_REQUIRED",
            )
        if raw.attestation_key_file is None:
            raise RunnerStop(
                Verdict.BLOCKED,
                "LIVE_FIXTURE_ATTESTATION_KEY_REQUIRED",
            )
        _verify_live_attestation(
            raw.live_fixture_attestation,
            raw.attestation_key_file,
            fixture,
        )
        live_fixture_verified = True
    return Config(
        environment=selected_environment,
        mode=mode,
        scenario=scenario,
        base_url=normalized_url,
        ingress_fingerprint=ingress_fingerprint,
        run_id=raw.run_id,
        output_path=raw.output_path,
        fixture=fixture,
        credentials=credentials,
        second_credentials=second_credentials,
        bounds=raw.bounds,
        live_fixture_verified=live_fixture_verified,
    )


def _parse_mode(value: str) -> Mode:
    try:
        return Mode(value)
    except ValueError as error:
        raise RunnerStop(Verdict.BLOCKED, "MODE_INVALID") from error


def _parse_scenario(value: str) -> Scenario:
    try:
        return Scenario(value)
    except ValueError as error:
        raise RunnerStop(Verdict.BLOCKED, "SCENARIO_INVALID") from error


def _select_base_url(argument: str | None, environment: str | None) -> str:
    selected_argument = _nonempty(argument)
    selected_environment = _nonempty(environment)
    if selected_argument is None and selected_environment is None:
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_URL_MISSING")
    if (
        selected_argument is not None
        and selected_environment is not None
        and selected_argument != selected_environment
    ):
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_URL_CONFLICT")
    return selected_argument or selected_environment or ""


def _normalize_base_url(value: str) -> str:
    parsed = urlsplit(value)
    try:
        parsed.port
    except ValueError as error:
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_URL_INVALID") from error
    hostname = parsed.hostname
    if (
        parsed.scheme not in {"http", "https"}
        or hostname is None
        or parsed.username is not None
        or parsed.password is not None
        or parsed.path not in {"", "/"}
        or parsed.query
        or parsed.fragment
    ):
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_URL_INVALID")
    _reject_service_dns(hostname.rstrip(".").lower())
    return urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))


def _reject_service_dns(hostname: str) -> None:
    try:
        ipaddress.ip_address(hostname)
    except ValueError:
        labels = tuple(hostname.split("."))
    else:
        return
    if (
        len(labels) == 1
        or labels[0] in _DIRECT_SERVICE_LABELS
        or "svc" in labels
    ):
        raise RunnerStop(
            Verdict.BLOCKED,
            "INGRESS_SERVICE_DNS_FORBIDDEN",
        )


def _verify_ingress_fingerprint(value: str | None, actual: str) -> None:
    expected = _nonempty(value)
    if expected is None:
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_IDENTITY_MISSING")
    if _FINGERPRINT_PATTERN.fullmatch(expected) is None:
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_IDENTITY_INVALID")
    if not hmac.compare_digest(expected, actual):
        raise RunnerStop(Verdict.BLOCKED, "INGRESS_IDENTITY_MISMATCH")


def _verify_bounds(bounds: Bounds) -> None:
    if not 1 <= bounds.max_attempts <= 3:
        raise RunnerStop(Verdict.BLOCKED, "RUNNER_BOUNDS_INVALID")
    if not 1 <= bounds.poll_attempts <= 20:
        raise RunnerStop(Verdict.BLOCKED, "RUNNER_BOUNDS_INVALID")
    if not 0 <= bounds.poll_interval_seconds <= 5:
        raise RunnerStop(Verdict.BLOCKED, "RUNNER_BOUNDS_INVALID")
    if not 0.1 <= bounds.timeout_seconds <= 30:
        raise RunnerStop(Verdict.BLOCKED, "RUNNER_BOUNDS_INVALID")


def _load_fixture(path: Path, run_id: str) -> Fixture:
    try:
        value = _JSON_ADAPTER.validate_json(path.read_bytes())
        manifest = _parse_manifest(value)
    except FileNotFoundError as error:
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_MANIFEST_MISSING") from error
    except (OSError, ValidationError, _Refusal) as error:
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_MANIFEST_INVALID") from error
    if manifest.run_id != run_id:
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_RUN_ID_MISMATCH")
    return Fixture(
        run_id=manifest.run_id,
        drop_id=FixtureId(manifest.fixture.drop_ref),
        product_id=FixtureId(manifest.fixture.product_ref),
        initial_stock=manifest.fixture.stock,
        subject_refs=(
            manifest.users[0].subject_ref,
            manifest.users[1].subject_ref,
        ),
    )


def _load_credentials(environment: Mapping[str, str]) -> Credentials:
    if _nonempty(environment.get("AWS_PURCHASE_JWT")) is not None:
        raise RunnerStop(Verdict.BLOCKED, "PREINJECTED_TOKEN_FORBIDDEN")
    email = _nonempty(environment.get("SYNTHETIC_CUSTOMER_EMAIL"))
    password = _nonempty(environment.get("SYNTHETIC_CUSTOMER_PASSWORD"))
    if email is None and password is None:
        raise RunnerStop(Verdict.BLOCKED, "CREDENTIALS_MISSING")
    if email is None or password is None:
        raise RunnerStop(Verdict.BLOCKED, "CREDENTIALS_INCOMPLETE")
    return Credentials(email=email, password=password)


def _load_second_credentials(
    environment: Mapping[str, str],
) -> Credentials | None:
    email = _nonempty(environment.get("SYNTHETIC_CUSTOMER_B_EMAIL"))
    password = _nonempty(environment.get("SYNTHETIC_CUSTOMER_B_PASSWORD"))
    if email is None and password is None:
        return None
    if email is None or password is None:
        raise RunnerStop(Verdict.BLOCKED, "SECOND_CREDENTIALS_INCOMPLETE")
    return Credentials(email=email, password=password)


def _verify_write_opt_in(value: str | None, run_id: str) -> None:
    expected = f"aws-dev:{run_id}:ALLOW_PURCHASE_WRITES"
    if value is None or not hmac.compare_digest(value, expected):
        raise RunnerStop(Verdict.BLOCKED, "AWS_DEV_WRITE_OPT_IN_REQUIRED")


def _verify_live_attestation(
    path: Path,
    key_path: Path,
    fixture: Fixture,
) -> None:
    try:
        value = _JSON_ADAPTER.validate_json(path.read_bytes())
        root = _mapping(value)
    except FileNotFoundError as error:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_REQUIRED",
        ) from error
    except (OSError, ValidationError) as error:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_INVALID",
        ) from error
    expected_root = {
        "schema_version",
        "environment",
        "run_id",
        "verdict",
        "api_traffic_allowed",
        "collector",
        "issued_at",
        "integrity",
        "users",
        "fixture",
    }
    if set(root) != expected_root:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_INVALID",
        )
    key = read_attestation_key(key_path)
    if key is None or not integrity_matches(root, key):
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_UNTRUSTED",
        )
    issued_at = root["issued_at"]
    if type(issued_at) is not str:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_INVALID",
        )
    try:
        issued = datetime.strptime(
            issued_at,
            "%Y-%m-%dT%H:%M:%SZ",
        ).replace(tzinfo=UTC)
    except ValueError as error:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_INVALID",
        ) from error
    age_seconds = (datetime.now(UTC) - issued).total_seconds()
    if (
        age_seconds > MAX_ATTESTATION_AGE_SECONDS
        or age_seconds < -MAX_ATTESTATION_FUTURE_SKEW_SECONDS
    ):
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_STALE",
        )
    users = _mapping(root["users"])
    fixture_state = _mapping(root["fixture"])
    expected_subjects = [fingerprint(value) for value in fixture.subject_refs]
    valid = (
        root["schema_version"] == 1
        and root["environment"] == "aws-dev"
        and root["run_id"] == fixture.run_id
        and root["verdict"] == "LIVE_FIXTURE_VERIFIED"
        and root["api_traffic_allowed"] is True
        and root["collector"] == COLLECTOR_ID
        and users.get("count") == 2
        and users.get("subject_fingerprints") == expected_subjects
        and users.get("credential_bindings") == "VERIFIED"
        and fixture_state.get("drop_fingerprint")
        == fingerprint(fixture.drop_id)
        and fixture_state.get("product_fingerprint")
        == fingerprint(fixture.product_id)
        and fixture_state.get("dedicated") is True
        and fixture_state.get("stock") == fixture.initial_stock
        and fixture_state.get("active_records") == 0
    )
    if not valid:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_BLOCKED",
        )


def _mapping(value: JsonValue) -> JsonObject:
    if type(value) is not dict:
        raise RunnerStop(
            Verdict.BLOCKED,
            "LIVE_FIXTURE_ATTESTATION_INVALID",
        )
    return value


def _nonempty(value: str | None) -> str | None:
    if value is None:
        return None
    normalized = value.strip()
    return normalized or None


def _sha256(value: str) -> str:
    import hashlib

    return hashlib.sha256(value.encode("utf-8")).hexdigest()
