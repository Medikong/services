from __future__ import annotations

import base64
import binascii
import hashlib
import re
from dataclasses import dataclass
from enum import StrEnum
from pathlib import Path
from typing import Final
from urllib.parse import urlsplit, urlunsplit


type JsonValue = (
    str | int | float | bool | None | list[JsonValue] | dict[str, JsonValue]
)
_RUN_ID_PATTERN: Final = re.compile(r"aws-purchase-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}")
_JWT_SEGMENT_PATTERN: Final = re.compile(r"[A-Za-z0-9_-]+")


class Verdict(StrEnum):
    VERIFIED = "VERIFIED"
    BLOCKED = "BLOCKED"
    FAIL = "FAIL"


@dataclass(frozen=True, slots=True)
class TokenCredential:
    token: str


@dataclass(frozen=True, slots=True)
class LoginCredential:
    email: str
    password: str


type Credential = TokenCredential | LoginCredential


@dataclass(frozen=True, slots=True)
class RunnerConfig:
    base_url: str
    ingress_fingerprint: str
    auth_route: str
    protected_route: str
    run_id: str
    json_output: Path
    junit_output: Path
    max_attempts: int
    backoff_seconds: float
    timeout_seconds: float


@dataclass(frozen=True, slots=True)
class StageRecord:
    name: str
    status: str
    attempts: int
    request_id: str
    idempotency_key: str

    def as_json(self) -> dict[str, JsonValue]:
        return {
            "name": self.name,
            "status": self.status,
            "attempts": self.attempts,
            "request_id": self.request_id,
            "idempotency_key": self.idempotency_key,
        }


@dataclass(frozen=True, slots=True)
class Report:
    run_id: str
    verdict: Verdict
    reason_code: str
    auth_mode: str
    ingress_fingerprint: str
    stages: tuple[StageRecord, ...]

    def as_json(self) -> dict[str, JsonValue]:
        return {
            "schema_version": 1,
            "run_id": self.run_id,
            "verdict": self.verdict.value,
            "reason_code": self.reason_code,
            "purchase_traffic_allowed": self.verdict is Verdict.VERIFIED,
            "purchase_requests_sent": 0,
            "auth_mode": self.auth_mode,
            "ingress_fingerprint": self.ingress_fingerprint,
            "stages": [stage.as_json() for stage in self.stages],
        }


class ConfigurationStop(Exception):
    def __init__(self, reason_code: str, fingerprint: str = "unconfigured") -> None:
        super().__init__(reason_code)
        self.reason_code = reason_code
        self.fingerprint = fingerprint


def build_config(
    *,
    base_url_argument: str | None,
    base_url_environment: str | None,
    auth_route: str,
    protected_route: str,
    run_id: str,
    json_output: Path,
    junit_output: Path,
    max_attempts: int,
    backoff_seconds: float,
    timeout_seconds: float,
) -> RunnerConfig:
    if _RUN_ID_PATTERN.fullmatch(run_id) is None:
        raise ConfigurationStop("RUN_ID_INVALID")
    base_url = _select_base_url(base_url_argument, base_url_environment)
    normalized_url = _normalize_base_url(base_url)
    fingerprint = ingress_fingerprint(normalized_url)
    _validate_route(auth_route, fingerprint)
    _validate_route(protected_route, fingerprint)
    if json_output.resolve() == junit_output.resolve():
        raise ConfigurationStop("OUTPUT_PATH_INVALID", fingerprint)
    if not 1 <= max_attempts <= 5:
        raise ConfigurationStop("RUNNER_BOUNDS_INVALID", fingerprint)
    if not 0 <= backoff_seconds <= 2:
        raise ConfigurationStop("RUNNER_BOUNDS_INVALID", fingerprint)
    if not 0.1 <= timeout_seconds <= 30:
        raise ConfigurationStop("RUNNER_BOUNDS_INVALID", fingerprint)
    return RunnerConfig(
        base_url=normalized_url,
        ingress_fingerprint=fingerprint,
        auth_route=auth_route,
        protected_route=protected_route,
        run_id=run_id,
        json_output=json_output,
        junit_output=junit_output,
        max_attempts=max_attempts,
        backoff_seconds=backoff_seconds,
        timeout_seconds=timeout_seconds,
    )


def load_credential(
    *,
    token: str | None,
    email: str | None,
    password: str | None,
) -> Credential:
    normalized_token = _nonempty(token)
    normalized_email = _nonempty(email)
    normalized_password = _nonempty(password)
    has_login_input = normalized_email is not None or normalized_password is not None
    if normalized_token is not None and has_login_input:
        raise ConfigurationStop("CREDENTIALS_AMBIGUOUS")
    if normalized_token is not None:
        if not is_jwt_shaped(normalized_token):
            raise ConfigurationStop("TOKEN_INVALID_FORMAT")
        return TokenCredential(token=normalized_token)
    if normalized_email is None and normalized_password is None:
        raise ConfigurationStop("CREDENTIALS_MISSING")
    if normalized_email is None or normalized_password is None:
        raise ConfigurationStop("CREDENTIALS_INCOMPLETE")
    return LoginCredential(email=normalized_email, password=normalized_password)


def is_jwt_shaped(token: str) -> bool:
    segments = token.split(".")
    if len(segments) != 3:
        return False
    for segment in segments:
        if _JWT_SEGMENT_PATTERN.fullmatch(segment) is None:
            return False
        padding = "=" * (-len(segment) % 4)
        try:
            base64.b64decode(segment + padding, altchars=b"-_", validate=True)
        except (binascii.Error, ValueError):
            return False
    return True


def ingress_fingerprint(base_url: str) -> str:
    digest = hashlib.sha256(base_url.encode("utf-8")).hexdigest()
    return f"sha256:{digest[:16]}"


def _select_base_url(argument: str | None, environment: str | None) -> str:
    normalized_argument = _nonempty(argument)
    normalized_environment = _nonempty(environment)
    if normalized_argument is None and normalized_environment is None:
        raise ConfigurationStop("INGRESS_URL_MISSING")
    if (
        normalized_argument is not None
        and normalized_environment is not None
        and normalized_argument != normalized_environment
    ):
        raise ConfigurationStop("INGRESS_URL_CONFLICT")
    return normalized_argument or normalized_environment or ""


def _normalize_base_url(value: str) -> str:
    parsed = urlsplit(value)
    try:
        port = parsed.port
    except ValueError as error:
        raise ConfigurationStop("INGRESS_URL_INVALID") from error
    hostname = parsed.hostname
    if (
        parsed.scheme not in {"http", "https"}
        or hostname is None
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path not in {"", "/"}
        or port is None
        and ":" in parsed.netloc
    ):
        raise ConfigurationStop("INGRESS_URL_INVALID")
    normalized_hostname = hostname.rstrip(".").lower()
    if normalized_hostname.endswith(".svc") or ".svc." in normalized_hostname:
        raise ConfigurationStop("INGRESS_SERVICE_DNS_FORBIDDEN")
    return urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))


def _validate_route(value: str, fingerprint: str) -> None:
    parsed = urlsplit(value)
    if (
        not value.startswith("/")
        or value.startswith("//")
        or parsed.scheme
        or parsed.netloc
        or parsed.query
        or parsed.fragment
    ):
        raise ConfigurationStop("ROUTE_PATH_INVALID", fingerprint)


def _nonempty(value: str | None) -> str | None:
    if value is None:
        return None
    normalized = value.strip()
    return normalized or None
