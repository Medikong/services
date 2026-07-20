#!/usr/bin/env python3
from __future__ import annotations

import base64
import binascii
import json
import os
import re
import secrets
import shlex
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from uuid import UUID, uuid4


ROOT = Path(__file__).resolve().parents[4]
COMMON_SCRIPTS = ROOT / "tests" / "e2e" / "scripts"
sys.path.insert(0, str(COMMON_SCRIPTS))

from auth_e2e_common import generate_rsa_private_key  # noqa: E402


REDIS_CLIENT_METRIC = "db_client_connections_use_time_milliseconds_count"
REDIS_SCOPE = "github.com/redis/go-redis/extra/redisotel"
POSTGRES_SCOPE = "github.com/exaring/otelpgx"
ROUTE = "/internal/session/status"
SESSION_STATUS_KEY_PREFIX = "auth:session-status:v2:"
OPERATIONAL_ROUTES = ("/healthz", "/readyz", "/metrics")
JWT_PATTERN = re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b")
EMAIL_PATTERN = re.compile(
    r"(?i)\b[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9-]+"
    r"(?:\.[a-z0-9-]+)*\.[a-z][a-z0-9-]*\b"
)


class CheckFailure(RuntimeError):
    """Describe one failed backend condition without carrying sensitive values."""

    def __init__(self, backend: str, condition: str) -> None:
        self.backend = backend
        self.condition = condition
        super().__init__(f"backend={backend} condition={condition}")


@dataclass(frozen=True)
class Settings:
    project: str
    compose_file: Path
    compose_command: tuple[str, ...]
    timeout_seconds: int
    poll_seconds: float
    auth_url: str
    admin_url: str
    tempo_url: str
    prometheus_url: str
    loki_url: str
    grafana_url: str
    collector_health_url: str
    alloy_url: str
    generated_key_path: Path
    configured_key_path: Path | None

    @classmethod
    def load(cls) -> Settings:
        """Load runner and host-port settings from environment variables."""
        project = _env("AUTH_OBS_E2E_PROJECT", "dropmong-auth-observability-e2e")
        if not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", project):
            raise CheckFailure("runner", "Compose project 이름이 안전하지 않습니다")
        configured_key = os.getenv("AUTH_OBS_JWT_PRIVATE_KEY_FILE", "").strip()
        return cls(
            project=project,
            compose_file=ROOT / "tests" / "e2e" / "auth-observability" / "docker-compose.yml",
            compose_command=tuple(shlex.split(_env("DOCKER_COMPOSE", "docker compose"))),
            timeout_seconds=_positive_int("AUTH_OBS_TIMEOUT_SECONDS", 120),
            poll_seconds=_positive_float("AUTH_OBS_POLL_SECONDS", 1.0),
            auth_url=_local_url("AUTH_OBS_AUTH_PORT", 18089),
            admin_url=_local_url("AUTH_OBS_ADMIN_PORT", 19090),
            tempo_url=_local_url("AUTH_OBS_TEMPO_PORT", 13200),
            prometheus_url=_local_url("AUTH_OBS_PROMETHEUS_PORT", 19091),
            loki_url=_local_url("AUTH_OBS_LOKI_PORT", 13100),
            grafana_url=_local_url("AUTH_OBS_GRAFANA_PORT", 13001),
            collector_health_url=_local_url("AUTH_OBS_COLLECTOR_HEALTH_PORT", 13134),
            alloy_url=_local_url("AUTH_OBS_ALLOY_PORT", 12346),
            generated_key_path=(
                ROOT / "tests" / "tmp" / "auth-observability-e2e" / project / "jwt.pem"
            ),
            configured_key_path=(Path(configured_key).expanduser().resolve() if configured_key else None),
        )


@dataclass(frozen=True)
class HTTPResult:
    status: int
    headers: dict[str, str]
    body: bytes


@dataclass(frozen=True)
class Fixture:
    email: str
    password: str
    user_id: str


@dataclass(frozen=True)
class Credentials:
    access_token: str
    refresh_token: str
    session_id: str
    flow_token: str
    trace_ids: tuple[str, str]


@dataclass(frozen=True)
class ScenarioResponse:
    name: str
    request_id: str
    trace_id: str
    status: int


@dataclass(frozen=True)
class Span:
    name: str
    trace_id: str
    span_id: str
    parent_span_id: str
    kind: str
    status: str
    scope: str
    attributes: dict[str, object]
    resource: dict[str, object]
    events: tuple[dict[str, object], ...]
    start_ns: int


@dataclass(frozen=True)
class TraceEvidence:
    scenario: ScenarioResponse
    server_span: Span
    spans: tuple[Span, ...]
    raw: dict[str, object]


@dataclass(frozen=True)
class MetricSnapshot:
    redis_up: float
    commands: float
    keyspace_hits: float
    keyspace_misses: float
    client_ok: float
    client_nil: float
    client_error: float


def _env(name: str, fallback: str) -> str:
    value = os.getenv(name, "").strip()
    return value or fallback


def _positive_int(name: str, fallback: int) -> int:
    raw = _env(name, str(fallback))
    try:
        value = int(raw)
    except ValueError as error:
        raise CheckFailure("runner", f"{name} 값이 정수가 아닙니다") from error
    if value <= 0:
        raise CheckFailure("runner", f"{name} 값은 0보다 커야 합니다")
    return value


def _positive_float(name: str, fallback: float) -> float:
    raw = _env(name, str(fallback))
    try:
        value = float(raw)
    except ValueError as error:
        raise CheckFailure("runner", f"{name} 값이 숫자가 아닙니다") from error
    if value <= 0:
        raise CheckFailure("runner", f"{name} 값은 0보다 커야 합니다")
    return value


def _local_url(port_name: str, fallback: int) -> str:
    return f"http://127.0.0.1:{_positive_int(port_name, fallback)}"


def _key_path(settings: Settings) -> Path:
    return settings.configured_key_path or settings.generated_key_path


def _ensure_key(settings: Settings) -> Path:
    path = _key_path(settings)
    if settings.configured_key_path is not None:
        if not path.is_file():
            raise CheckFailure("fixture", "지정한 RSA 개인 키 파일을 찾을 수 없습니다")
        return path
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)
    if path.is_file():
        path.chmod(0o444)
        return path
    try:
        generate_rsa_private_key(path, mode=0o444)
    except (OSError, subprocess.SubprocessError) as error:
        raise CheckFailure("fixture", "RSA 테스트 키를 생성하지 못했습니다") from error
    return path


def _remove_generated_key(settings: Settings) -> None:
    if settings.configured_key_path is not None:
        return
    settings.generated_key_path.unlink(missing_ok=True)
    try:
        settings.generated_key_path.parent.rmdir()
    except OSError:
        pass


def _compose_env(settings: Settings, key_path: Path | None = None) -> dict[str, str]:
    environment = os.environ.copy()
    environment["AUTH_OBS_E2E_PROJECT"] = settings.project
    effective_key = key_path or _key_path(settings)
    if not effective_key.exists():
        effective_key = Path("/dev/null")
    environment["AUTH_OBS_JWT_PRIVATE_KEY_FILE"] = str(effective_key)
    return environment


def _compose(
    settings: Settings,
    *arguments: str,
    input_text: str | None = None,
    capture: bool = False,
    check: bool = True,
    condition: str = "Compose 명령이 실패했습니다",
) -> subprocess.CompletedProcess[str]:
    command = [
        *settings.compose_command,
        "-p",
        settings.project,
        "-f",
        str(settings.compose_file),
        *arguments,
    ]
    result = subprocess.run(
        command,
        cwd=ROOT,
        env=_compose_env(settings),
        input=input_text,
        text=True,
        capture_output=capture,
        check=False,
    )
    if check and result.returncode != 0:
        raise CheckFailure("Docker Compose", condition)
    return result


def _labeled_ids(settings: Settings, resource: str) -> list[str]:
    commands = {
        "container": ["docker", "ps", "-aq"],
        "network": ["docker", "network", "ls", "-q"],
        "volume": ["docker", "volume", "ls", "-q"],
    }
    result = subprocess.run(
        [
            *commands[resource],
            "--filter",
            f"label=com.docker.compose.project={settings.project}",
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise CheckFailure("Docker", "Compose 잔여 자원 조회에 실패했습니다")
    return [line for line in result.stdout.splitlines() if line.strip()]


def _cleanup(settings: Settings) -> None:
    result = _compose(
        settings,
        "down",
        "-v",
        "--remove-orphans",
        capture=True,
        check=False,
    )
    if result.returncode != 0:
        raise CheckFailure("Docker Compose", "관측성 stack 정리 명령이 실패했습니다")
    residue = {
        resource: _labeled_ids(settings, resource)
        for resource in ("container", "network", "volume")
    }
    if any(residue.values()):
        counts = ", ".join(
            f"{resource}={len(values)}" for resource, values in residue.items() if values
        )
        raise CheckFailure("Docker", f"정리 후 Compose 자원이 남았습니다: {counts}")


def _request(
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    payload: dict[str, object] | None = None,
    timeout: float = 8,
    backend: str,
) -> HTTPResult:
    data = json.dumps(payload, separators=(",", ":")).encode() if payload is not None else None
    request_headers = dict(headers or {})
    if payload is not None:
        request_headers["Content-Type"] = "application/json"
    request = Request(url, data=data, headers=request_headers, method=method)
    try:
        with urlopen(request, timeout=timeout) as response:
            return HTTPResult(
                status=response.status,
                headers={key.lower(): value for key, value in response.headers.items()},
                body=response.read(),
            )
    except HTTPError as error:
        return HTTPResult(
            status=error.code,
            headers={key.lower(): value for key, value in error.headers.items()},
            body=error.read(),
        )
    except (OSError, URLError) as error:
        raise CheckFailure(backend, "HTTP endpoint에 연결하지 못했습니다") from error


def _json(result: HTTPResult, backend: str) -> dict[str, Any]:
    try:
        payload = json.loads(result.body)
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise CheckFailure(backend, "JSON 응답 형식이 올바르지 않습니다") from error
    if not isinstance(payload, dict):
        raise CheckFailure(backend, "JSON 응답이 객체가 아닙니다")
    return payload


def _wait_status(
    settings: Settings,
    backend: str,
    url: str,
    expected: int = 200,
) -> HTTPResult:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        try:
            result = _request("GET", url, backend=backend)
        except CheckFailure:
            time.sleep(settings.poll_seconds)
            continue
        if result.status == expected:
            return result
        time.sleep(settings.poll_seconds)
    raise CheckFailure(backend, f"제한 시간 안에 HTTP {expected} 상태가 되지 않았습니다")


def _wait_stack(settings: Settings) -> None:
    endpoints = (
        ("Auth readiness", f"{settings.admin_url}/readyz"),
        ("Tempo", f"{settings.tempo_url}/ready"),
        ("Prometheus", f"{settings.prometheus_url}/-/ready"),
        ("Loki", f"{settings.loki_url}/ready"),
        ("Grafana", f"{settings.grafana_url}/api/health"),
        ("OTel Collector", f"{settings.collector_health_url}/"),
        ("Alloy", f"{settings.alloy_url}/-/ready"),
    )
    for backend, url in endpoints:
        _wait_status(settings, backend, url)


def _seed_fixture(settings: Settings) -> Fixture:
    user_id, identity_id, link_id = uuid4(), uuid4(), uuid4()
    credential_id, status_change_id = uuid4(), uuid4()
    email = f"auth-observability-{identity_id.hex}@example.test"
    password = f"Obs-{secrets.token_urlsafe(24)}!42"
    sql = f"""
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO auth_identities (
    identity_id, identity_type, identity_namespace, normalized_value, masked_value,
    status, verification_status, credential_status, verified_at
) VALUES (
    '{identity_id}', 'email', 'default', '{email}', 'a***@example.test',
    'verified', 'verified', 'active', now()
);
INSERT INTO auth_identity_links (
    identity_link_id, identity_id, identity_type, user_id, link_status, link_reason,
    activated_at
) VALUES ('{link_id}', '{identity_id}', 'email', '{user_id}', 'active', 'signup', now());
INSERT INTO auth_password_credentials (
    password_credential_id, identity_id, password_hash, password_status, hash_algorithm,
    created_at, updated_at
) VALUES (
    '{credential_id}', '{identity_id}', crypt('{password}', gen_salt('bf', 10)),
    'active', 'bcrypt', now(), now()
);
INSERT INTO auth_user_auth_states (
    user_id, status, user_version, status_change_id, effective_at
) VALUES ('{user_id}', 'active', 1, '{status_change_id}', now());
COMMIT;
"""
    _compose(
        settings,
        "exec",
        "-T",
        "postgres",
        "psql",
        "-U",
        "app",
        "-d",
        "auth_service",
        "-v",
        "ON_ERROR_STOP=1",
        input_text=sql,
        capture=True,
        condition="Auth 로그인 fixture 생성에 실패했습니다",
    )
    return Fixture(email=email, password=password, user_id=str(user_id))


def _data_field(payload: dict[str, Any], *path: str) -> str:
    current: object = payload.get("data", payload)
    for key in path:
        if not isinstance(current, dict):
            raise CheckFailure("Auth fixture", "필요한 응답 필드를 찾지 못했습니다")
        current = current.get(key)
    if not isinstance(current, str) or not current:
        raise CheckFailure("Auth fixture", "필요한 응답 필드를 찾지 못했습니다")
    return current


def _issue_credentials(settings: Settings, fixture: Fixture) -> Credentials:
    intent = _request(
        "POST",
        f"{settings.auth_url}/api/v1/auth/intents",
        headers={
            "X-Client-Channel": "ios",
            "Idempotency-Key": str(uuid4()),
            "X-Request-Id": str(uuid4()),
        },
        payload={"returnPath": "/e2e", "intentType": "navigation"},
        backend="Auth fixture",
    )
    if intent.status != 201:
        raise CheckFailure("Auth fixture", "인증 intent 생성 응답이 HTTP 201이 아닙니다")
    intent_trace_id = _required_trace_header(intent, "인증 intent")
    intent_payload = _json(intent, "Auth fixture")
    intent_id = _data_field(intent_payload, "authIntentId")
    flow_token = _data_field(intent_payload, "authFlowToken")

    signin = _request(
        "POST",
        f"{settings.auth_url}/api/v1/auth/signins/email",
        headers={
            "X-Auth-Flow-Token": flow_token,
            "Idempotency-Key": str(uuid4()),
            "X-Request-Id": str(uuid4()),
        },
        payload={
            "authIntentId": intent_id,
            "email": fixture.email,
            "password": fixture.password,
            "rememberMe": False,
        },
        backend="Auth fixture",
    )
    if signin.status != 200:
        raise CheckFailure("Auth fixture", "실제 이메일 로그인 응답이 HTTP 200이 아닙니다")
    signin_trace_id = _required_trace_header(signin, "이메일 로그인")
    signin_payload = _json(signin, "Auth fixture")
    session_id = _data_field(signin_payload, "session", "sessionId")
    try:
        UUID(session_id)
    except ValueError as error:
        raise CheckFailure("Auth fixture", "발급된 Session ID가 UUID가 아닙니다") from error
    return Credentials(
        access_token=_data_field(signin_payload, "tokens", "accessToken"),
        refresh_token=_data_field(signin_payload, "tokens", "refreshToken"),
        session_id=session_id,
        flow_token=flow_token,
        trace_ids=(intent_trace_id, signin_trace_id),
    )


def _required_trace_header(result: HTTPResult, label: str) -> str:
    trace_id = result.headers.get("x-trace-id", "").lower()
    if not re.fullmatch(r"[0-9a-f]{32}", trace_id):
        raise CheckFailure("Auth fixture", f"{label} 응답에 유효한 X-Trace-Id가 없습니다")
    return trace_id


def _delete_session_cache(
    settings: Settings,
    session_id: str,
    *,
    require_existing: bool = False,
) -> None:
    result = _compose(
        settings,
        "exec",
        "-T",
        "redis",
        "valkey-cli",
        "-x",
        "DEL",
        input_text=f"{SESSION_STATUS_KEY_PREFIX}{session_id}",
        capture=True,
        condition="Session cache 삭제에 실패했습니다",
    )
    if result.stdout.strip() not in {"0", "1"}:
        raise CheckFailure("Redis fixture", "Session cache 삭제 결과를 확인하지 못했습니다")
    if require_existing and result.stdout.strip() != "1":
        raise CheckFailure("Redis fixture", "결정적 miss 준비를 위한 기존 cache 삭제가 없었습니다")


def _postgres_boolean(settings: Settings, sql: str, condition: str) -> bool:
    result = _compose(
        settings,
        "exec",
        "-T",
        "postgres",
        "psql",
        "-U",
        "app",
        "-d",
        "auth_service",
        "-v",
        "ON_ERROR_STOP=1",
        "-qAt",
        input_text=sql,
        capture=True,
        condition=condition,
    )
    value = result.stdout.strip()
    if value not in {"t", "f"}:
        raise CheckFailure("PostgreSQL projection", "비민감 boolean 판정 결과가 올바르지 않습니다")
    return value == "t"


def _wait_postgres_boolean(settings: Settings, sql: str, condition: str) -> None:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        if _postgres_boolean(settings, sql, condition):
            return
        time.sleep(settings.poll_seconds)
    raise CheckFailure("PostgreSQL projection", condition)


def _revoke_session_in_postgres(settings: Settings, session_id: str) -> None:
    canonical_session_id = str(UUID(session_id))
    sql = f"""
BEGIN;
UPDATE auth_sessions
SET session_status = 'revoked',
    revoked_at = now(),
    revocation_reason = 'observability_e2e',
    updated_at = now()
WHERE session_id = '{canonical_session_id}'::uuid
  AND session_status = 'active';
COMMIT;
SELECT EXISTS (
    SELECT 1
    FROM auth_sessions AS sessions
    JOIN auth_session_status_projection_jobs AS jobs
      ON jobs.session_id = sessions.session_id
     AND jobs.session_version = sessions.row_version
    WHERE sessions.session_id = '{canonical_session_id}'::uuid
      AND sessions.session_status = 'revoked'
      AND jobs.target_status = 'revoked'
      AND jobs.delivery_status IN ('pending', 'processing')
);
"""
    if not _postgres_boolean(
        settings,
        sql,
        "Redis 장애 중 Session 폐기와 projection 작업 생성에 실패했습니다",
    ):
        raise CheckFailure(
            "PostgreSQL projection",
            "Redis 장애 중 Session 폐기와 projection 작업이 원자적으로 남지 않았습니다",
        )


def _wait_projection_retry(settings: Settings, session_id: str) -> None:
    canonical_session_id = str(UUID(session_id))
    _wait_postgres_boolean(
        settings,
        f"""
SELECT EXISTS (
    SELECT 1
    FROM auth_session_status_projection_jobs
    WHERE session_id = '{canonical_session_id}'::uuid
      AND target_status = 'revoked'
      AND (
        (delivery_status = 'pending'
         AND attempt_count >= 1
         AND last_error_code = 'projection_apply_failed')
        OR (delivery_status = 'processing' AND attempt_count >= 2)
      )
);
""",
        "Redis 장애 중 worker의 projection 재시도 기록을 확인하지 못했습니다",
    )


def _wait_projection_delivered(settings: Settings, session_id: str) -> None:
    canonical_session_id = str(UUID(session_id))
    _wait_postgres_boolean(
        settings,
        f"""
SELECT EXISTS (
    SELECT 1
    FROM auth_session_status_projection_jobs
    WHERE session_id = '{canonical_session_id}'::uuid
      AND target_status = 'revoked'
      AND delivery_status = 'delivered'
      AND attempt_count >= 2
      AND delivered_at IS NOT NULL
      AND lease_owner IS NULL
      AND lease_until IS NULL
      AND last_error_code IS NULL
);
""",
        "Redis 복구 후 worker가 projection 작업을 완료하지 못했습니다",
    )


def _revoked_status_request(settings: Settings, access_token: str) -> ScenarioResponse:
    request_id = str(uuid4())
    result = _request(
        "GET",
        f"{settings.auth_url}{ROUTE}",
        headers={"Authorization": f"Bearer {access_token}", "X-Request-Id": request_id},
        backend="Auth projection",
    )
    if result.status != 401:
        raise CheckFailure("Auth projection", "폐기된 Session status 응답이 HTTP 401이 아닙니다")
    payload = _json(result, "Auth projection")
    if payload.get("code") != "AUTH_SESSION_REVOKED":
        raise CheckFailure("Auth projection", "폐기된 Session의 공개 오류 code가 다릅니다")
    if payload.get("requestId") != request_id:
        raise CheckFailure("Auth projection", "폐기 응답의 request ID가 요청과 다릅니다")
    return ScenarioResponse(
        name="projection 폐기",
        request_id=request_id,
        trace_id=_required_trace_header(result, "projection 폐기"),
        status=result.status,
    )


def _verify_projection_retry(settings: Settings, credentials: Credentials) -> ScenarioResponse:
    redis_stopped = False
    try:
        _compose(settings, "stop", "redis", condition="projection 재시도용 Redis 중단에 실패했습니다")
        redis_stopped = True
        _revoke_session_in_postgres(settings, credentials.session_id)
        _wait_projection_retry(settings, credentials.session_id)
    finally:
        if redis_stopped:
            _compose(
                settings,
                "start",
                "redis",
                condition="projection 재시도용 Redis 복구 시작에 실패했습니다",
            )
    _wait_status(settings, "Auth readiness", f"{settings.admin_url}/readyz")
    _wait_projection_delivered(settings, credentials.session_id)
    return _revoked_status_request(settings, credentials.access_token)


def _scenario_request(
    settings: Settings,
    name: str,
    access_token: str,
    expected_status: int,
) -> ScenarioResponse:
    request_id = str(uuid4())
    result = _request(
        "GET",
        f"{settings.auth_url}{ROUTE}",
        headers={
            "Authorization": f"Bearer {access_token}",
            "X-Request-Id": request_id,
        },
        backend=f"Auth {name}",
    )
    if result.status != expected_status:
        raise CheckFailure("Auth", f"{name} 응답이 HTTP {expected_status}가 아닙니다")
    if result.headers.get("x-request-id") != request_id:
        raise CheckFailure("Auth", f"{name} 응답의 request ID가 요청과 다릅니다")
    trace_id = result.headers.get("x-trace-id", "").lower()
    if not re.fullmatch(r"[0-9a-f]{32}", trace_id):
        raise CheckFailure("Auth", f"{name} 응답에 유효한 X-Trace-Id가 없습니다")
    return ScenarioResponse(
        name=name,
        request_id=request_id,
        trace_id=trace_id,
        status=result.status,
    )


def _decode_attribute(value: object) -> object:
    if not isinstance(value, dict):
        return None
    for key in ("stringValue", "intValue", "doubleValue", "boolValue"):
        if key in value:
            return value[key]
    array = value.get("arrayValue")
    if isinstance(array, dict) and isinstance(array.get("values"), list):
        return [_decode_attribute(item) for item in array["values"]]
    return None


def _attributes(raw: object) -> dict[str, object]:
    if not isinstance(raw, list):
        return {}
    result: dict[str, object] = {}
    for item in raw:
        if not isinstance(item, dict) or not isinstance(item.get("key"), str):
            continue
        result[item["key"]] = _decode_attribute(item.get("value"))
    return result


def _normalize_id(raw: object, byte_length: int) -> str:
    if not isinstance(raw, str) or not raw:
        return ""
    lowered = raw.lower()
    if re.fullmatch(rf"[0-9a-f]{{{byte_length * 2}}}", lowered):
        return lowered
    try:
        decoded = base64.b64decode(raw + "=" * (-len(raw) % 4), validate=True)
    except (ValueError, binascii.Error):
        return ""
    return decoded.hex() if len(decoded) == byte_length else ""


def _status_code(raw: object) -> str:
    if not isinstance(raw, dict):
        return "UNSET"
    value = raw.get("code", "UNSET")
    return str(value).upper()


def _span_items(payload: dict[str, object]) -> tuple[Span, ...]:
    resources = payload.get("resourceSpans")
    if not isinstance(resources, list):
        resources = payload.get("batches")
    if not isinstance(resources, list):
        return ()
    spans: list[Span] = []
    for resource_item in resources:
        if not isinstance(resource_item, dict):
            continue
        resource = resource_item.get("resource")
        resource_attrs = _attributes(resource.get("attributes") if isinstance(resource, dict) else None)
        scopes = resource_item.get("scopeSpans")
        if not isinstance(scopes, list):
            scopes = resource_item.get("instrumentationLibrarySpans")
        if not isinstance(scopes, list):
            continue
        for scope_item in scopes:
            if not isinstance(scope_item, dict):
                continue
            scope = scope_item.get("scope") or scope_item.get("instrumentationLibrary")
            scope_name = scope.get("name", "") if isinstance(scope, dict) else ""
            raw_spans = scope_item.get("spans")
            if not isinstance(raw_spans, list):
                continue
            for raw_span in raw_spans:
                if not isinstance(raw_span, dict):
                    continue
                raw_events = raw_span.get("events")
                events = tuple(item for item in raw_events if isinstance(item, dict)) if isinstance(raw_events, list) else ()
                try:
                    start_ns = int(raw_span.get("startTimeUnixNano", 0))
                except (TypeError, ValueError):
                    start_ns = 0
                spans.append(
                    Span(
                        name=str(raw_span.get("name", "")),
                        trace_id=_normalize_id(raw_span.get("traceId"), 16),
                        span_id=_normalize_id(raw_span.get("spanId"), 8),
                        parent_span_id=_normalize_id(raw_span.get("parentSpanId"), 8),
                        kind=str(raw_span.get("kind", "")).upper(),
                        status=_status_code(raw_span.get("status")),
                        scope=str(scope_name),
                        attributes=_attributes(raw_span.get("attributes")),
                        resource=resource_attrs,
                        events=events,
                        start_ns=start_ns,
                    )
                )
    return tuple(spans)


def _trace_is_complete(
    payload: dict[str, object],
    scenario: ScenarioResponse,
    mode: str,
) -> bool:
    spans = _span_items(payload)
    servers = [
        span
        for span in spans
        if span.name == f"GET {ROUTE}"
        and span.attributes.get("request_id") == scenario.request_id
        and _is_server(span)
    ]
    redis_spans = _redis_spans(spans)
    if len(servers) != 1 or not any(span.name == "get" for span in redis_spans):
        return False
    if mode == "miss":
        return bool(_postgres_spans(spans)) and any(
            span.name in {"evalsha", "eval"} for span in redis_spans
        )
    return True


def _wait_trace(
    settings: Settings,
    scenario: ScenarioResponse,
    mode: str,
) -> dict[str, object]:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        result = _request(
            "GET",
            f"{settings.tempo_url}/api/traces/{scenario.trace_id}",
            backend="Tempo",
        )
        if result.status == 200:
            payload = _json(result, "Tempo")
            if _trace_is_complete(payload, scenario, mode):
                return payload
        elif result.status != 404:
            raise CheckFailure("Tempo", f"{scenario.name} trace 조회가 실패했습니다")
        time.sleep(settings.poll_seconds)
    raise CheckFailure(
        "Tempo",
        f"{scenario.name} trace의 필수 span 구조가 제한 시간 안에 완성되지 않았습니다",
    )


def _is_server(span: Span) -> bool:
    return span.kind in {"2", "SPAN_KIND_SERVER", "SERVER"}


def _is_error(span: Span) -> bool:
    return span.status in {"2", "STATUS_CODE_ERROR", "ERROR"}


def _redis_spans(spans: tuple[Span, ...]) -> list[Span]:
    return [span for span in spans if span.scope == REDIS_SCOPE]


def _postgres_spans(spans: tuple[Span, ...]) -> list[Span]:
    return [
        span
        for span in spans
        if span.scope == POSTGRES_SCOPE
        or span.attributes.get("db.system.name") == "postgresql"
    ]


def _validate_trace(
    settings: Settings,
    scenario: ScenarioResponse,
    mode: str,
) -> TraceEvidence:
    raw = _wait_trace(settings, scenario, mode)
    spans = _span_items(raw)
    if not spans:
        raise CheckFailure("Tempo", f"{scenario.name} trace에 span이 없습니다")
    if any(span.trace_id and span.trace_id != scenario.trace_id for span in spans):
        raise CheckFailure("Tempo", f"{scenario.name} trace ID가 응답 헤더와 다릅니다")
    servers = [
        span
        for span in spans
        if span.name == f"GET {ROUTE}"
        and span.attributes.get("request_id") == scenario.request_id
        and _is_server(span)
    ]
    if len(servers) != 1:
        raise CheckFailure("Tempo", f"{scenario.name} HTTP server span을 하나로 식별하지 못했습니다")
    server = servers[0]
    if server.resource.get("service.name") != "auth-service":
        raise CheckFailure("Tempo", f"{scenario.name} service.name이 auth-service가 아닙니다")
    if server.attributes.get("http.route") != ROUTE:
        raise CheckFailure("Tempo", f"{scenario.name} HTTP route 속성이 다릅니다")

    redis_spans = _redis_spans(spans)
    get_spans = [span for span in redis_spans if span.name == "get"]
    if len(get_spans) != 1 or get_spans[0].parent_span_id != server.span_id:
        raise CheckFailure("Tempo", f"{scenario.name} Redis GET이 HTTP span의 직접 자식이 아닙니다")
    for span in redis_spans:
        if "db.statement" in span.attributes or "db.query.text" in span.attributes:
            raise CheckFailure("Tempo", "Redis span에 명령 인자나 key/value가 노출됐습니다")

    postgres_spans = _postgres_spans(spans)
    pipelines = [span for span in redis_spans if span.name.startswith("redis.pipeline ")]
    cas_spans = [span for span in redis_spans if span.name in {"evalsha", "eval"}]
    if mode == "miss":
        selects = [
            span
            for span in postgres_spans
            if str(span.attributes.get("db.operation.name", "")).upper() == "SELECT"
        ]
        write_pipelines = [
            span
            for span in pipelines
            if all(command in span.name for command in ("sadd", "expire"))
        ]
        if not selects:
            raise CheckFailure("Tempo", "cache miss trace에 PostgreSQL 조회가 없습니다")
        if not cas_spans or not any(not _is_error(span) for span in cas_spans):
            raise CheckFailure("Tempo", "cache miss trace에 Redis CAS write-through가 없습니다")
        if len(write_pipelines) != 1:
            raise CheckFailure("Tempo", "cache miss trace에 Redis reverse-index write가 없습니다")
        pipeline = write_pipelines[0]
        if pipeline.parent_span_id != server.span_id or any(
            span.parent_span_id != server.span_id for span in cas_spans
        ):
            raise CheckFailure("Tempo", "Redis write-through가 HTTP span의 직접 자식이 아닙니다")
        successful_cas = next(span for span in reversed(cas_spans) if not _is_error(span))
        if all(span.start_ns for span in (get_spans[0], selects[0], successful_cas, pipeline)) and not (
            get_spans[0].start_ns
            <= selects[0].start_ns
            <= successful_cas.start_ns
            <= pipeline.start_ns
        ):
            raise CheckFailure("Tempo", "miss의 Redis-PostgreSQL-CAS-write-through 순서가 다릅니다")
    elif mode == "hit":
        if postgres_spans:
            raise CheckFailure("Tempo", "cache hit trace에 PostgreSQL 조회가 포함됐습니다")
        if pipelines:
            raise CheckFailure("Tempo", "cache hit trace에 Redis write-through가 포함됐습니다")
        if cas_spans:
            raise CheckFailure("Tempo", "cache hit trace에 Redis CAS write-through가 포함됐습니다")
    elif mode == "fault":
        if not _is_error(get_spans[0]) or not get_spans[0].events:
            raise CheckFailure("Tempo", "Redis 장애 GET span에 error 상태와 event가 없습니다")
        if not _is_error(server):
            raise CheckFailure("Tempo", "Redis 장애 HTTP span이 error 상태가 아닙니다")
    else:
        raise CheckFailure("runner", "알 수 없는 trace 검증 mode입니다")
    return TraceEvidence(scenario=scenario, server_span=server, spans=spans, raw=raw)


def _prometheus_sample(settings: Settings, query: str) -> tuple[float, float] | None:
    params = urlencode({"query": query})
    result = _request(
        "GET",
        f"{settings.prometheus_url}/api/v1/query?{params}",
        backend="Prometheus",
    )
    if result.status != 200:
        raise CheckFailure("Prometheus", "instant query API가 HTTP 200을 반환하지 않았습니다")
    payload = _json(result, "Prometheus")
    data = payload.get("data")
    rows = data.get("result") if isinstance(data, dict) else None
    if not isinstance(rows, list) or not rows:
        return None
    value = rows[0].get("value") if isinstance(rows[0], dict) else None
    if not isinstance(value, list) or len(value) != 2:
        raise CheckFailure("Prometheus", "instant query 응답 값이 올바르지 않습니다")
    try:
        return float(value[1]), float(value[0])
    except (TypeError, ValueError) as error:
        raise CheckFailure("Prometheus", "instant query 숫자 값을 읽지 못했습니다") from error


def _prometheus_value(settings: Settings, query: str) -> float:
    sample = _prometheus_sample(settings, query)
    return 0.0 if sample is None else sample[0]


def _server_metric_queries() -> dict[str, str]:
    return {
        "redis_up": 'max(redis_up{job="redis-exporter"})',
        "commands": 'sum(redis_commands_processed_total{job="redis-exporter"})',
        "keyspace_hits": 'sum(redis_keyspace_hits_total{job="redis-exporter"})',
        "keyspace_misses": 'sum(redis_keyspace_misses_total{job="redis-exporter"})',
    }


def _client_metric_query(metric_type: str, status: str) -> str:
    return (
        f'sum({REDIS_CLIENT_METRIC}{{job="auth-service",db_system="redis",'
        f'type="{metric_type}",status="{status}"}})'
    )


def _metric_snapshot(settings: Settings, not_before: float) -> MetricSnapshot:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        last_scrape = _prometheus_value(
            settings,
            'max(timestamp(redis_up{job="redis-exporter"}))',
        )
        if last_scrape >= not_before:
            break
        time.sleep(settings.poll_seconds)
    else:
        raise CheckFailure("Prometheus", "redis_exporter metric이 새 scrape로 갱신되지 않았습니다")
    server_values: dict[str, float] = {}
    for name, query in _server_metric_queries().items():
        sample = _prometheus_sample(settings, query)
        if sample is None:
            raise CheckFailure("Prometheus", f"redis_exporter {name} metric이 없습니다")
        server_values[name] = sample[0]
    return MetricSnapshot(
        redis_up=server_values["redis_up"],
        commands=server_values["commands"],
        keyspace_hits=server_values["keyspace_hits"],
        keyspace_misses=server_values["keyspace_misses"],
        client_ok=_prometheus_value(settings, _client_metric_query("command", "ok")),
        client_nil=_prometheus_value(settings, _client_metric_query("command", "nil")),
        client_error=_prometheus_value(settings, _client_metric_query("command", "error")),
    )


def _wait_metrics(
    settings: Settings,
    not_before: float,
    condition: str,
    predicate: Callable[[MetricSnapshot], bool],
) -> MetricSnapshot:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        snapshot = _metric_snapshot(settings, not_before)
        if predicate(snapshot):
            return snapshot
        time.sleep(settings.poll_seconds)
    raise CheckFailure("Prometheus", condition)


def _wait_outage_metrics(
    settings: Settings,
    not_before: float,
    previous_client_errors: float,
) -> None:
    deadline = time.monotonic() + settings.timeout_seconds
    error_timestamp_query = (
        f"max(timestamp({REDIS_CLIENT_METRIC}"
        '{job="auth-service",db_system="redis",type="command",status="error"}))'
    )
    while time.monotonic() < deadline:
        redis_up = _prometheus_value(settings, 'max(redis_up{job="redis-exporter"})')
        client_errors = _prometheus_value(
            settings,
            _client_metric_query("command", "error"),
        )
        error_scrape = _prometheus_value(settings, error_timestamp_query)
        if (
            redis_up == 0
            and client_errors > previous_client_errors
            and error_scrape >= not_before
        ):
            return
        time.sleep(settings.poll_seconds)
    raise CheckFailure(
        "Prometheus",
        "Redis 장애 client error metric과 redis_up=0을 확인하지 못했습니다",
    )


def _assert_auth_metric(settings: Settings) -> None:
    result = _request("GET", f"{settings.admin_url}/metrics", backend="Auth metrics")
    if result.status != 200:
        raise CheckFailure("Auth metrics", "/metrics가 HTTP 200을 반환하지 않았습니다")
    text = result.body.decode("utf-8", errors="replace")
    if f"# TYPE {REDIS_CLIENT_METRIC.removesuffix('_count')} histogram" not in text:
        raise CheckFailure("Auth metrics", f"고정한 Redis metric {REDIS_CLIENT_METRIC}이 없습니다")


def _assert_prometheus_targets(settings: Settings) -> None:
    deadline = time.monotonic() + settings.timeout_seconds
    queries = (
        'max(up{job="auth-service"})',
        'max(up{job="redis-exporter"})',
        'max(target_info{job="auth-service",service_name="auth-service"})',
    )
    while time.monotonic() < deadline:
        if all(_prometheus_value(settings, query) == 1 for query in queries):
            return
        time.sleep(settings.poll_seconds)
    raise CheckFailure("Prometheus", "Auth와 redis_exporter scrape target이 정상화되지 않았습니다")


def _tempo_search(settings: Settings, request_id: str) -> list[object]:
    params = urlencode(
        {"tags": f"service.name=auth-service request_id={request_id}", "limit": "20"}
    )
    result = _request(
        "GET",
        f"{settings.tempo_url}/api/search?{params}",
        backend="Tempo",
    )
    if result.status != 200:
        raise CheckFailure("Tempo", "trace 검색 API가 HTTP 200을 반환하지 않았습니다")
    traces = _json(result, "Tempo").get("traces")
    if not isinstance(traces, list):
        raise CheckFailure("Tempo", "trace 검색 응답 형식이 올바르지 않습니다")
    return traces


def _assert_operational_traces_excluded(
    settings: Settings,
    access_token: str,
) -> TraceEvidence:
    request_ids: list[str] = []
    for path in OPERATIONAL_ROUTES:
        request_id = str(uuid4())
        request_ids.append(request_id)
        result = _request(
            "GET",
            f"{settings.admin_url}{path}",
            headers={"X-Request-Id": request_id},
            backend="Auth operational endpoint",
        )
        if result.status != 200:
            raise CheckFailure("Auth", f"{path}가 HTTP 200을 반환하지 않았습니다")
        if result.headers.get("x-trace-id"):
            raise CheckFailure("Auth", f"{path} 응답에 X-Trace-Id가 생겼습니다")
    barrier = _scenario_request(settings, "trace 저장 barrier", access_token, 200)
    barrier_trace = _validate_trace(settings, barrier, "miss")
    if any(_tempo_search(settings, request_id) for request_id in request_ids):
        raise CheckFailure("Tempo", "operational endpoint의 HTTP trace가 저장됐습니다")
    return barrier_trace


def _all_auth_trace_payloads(
    settings: Settings,
    required_trace_ids: set[str],
) -> list[dict[str, object]]:
    params = urlencode({"tags": "service.name=auth-service", "limit": "1000"})
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        result = _request(
            "GET",
            f"{settings.tempo_url}/api/search?{params}",
            backend="Tempo",
        )
        if result.status != 200:
            raise CheckFailure("Tempo", "Auth trace 전체 검색 API가 실패했습니다")
        rows = _json(result, "Tempo").get("traces")
        if not isinstance(rows, list):
            raise CheckFailure("Tempo", "Auth trace 전체 검색 응답 형식이 올바르지 않습니다")
        trace_ids = {
            str(row.get("traceID") or row.get("traceId")).lower()
            for row in rows
            if isinstance(row, dict) and (row.get("traceID") or row.get("traceId"))
        }
        trace_ids.update(required_trace_ids)
        payloads: list[dict[str, object]] = []
        complete = True
        for trace_id in sorted(trace_ids):
            detail = _request(
                "GET",
                f"{settings.tempo_url}/api/traces/{trace_id}",
                backend="Tempo",
            )
            if detail.status != 200:
                complete = False
                break
            payloads.append(_json(detail, "Tempo"))
        if complete:
            return payloads
        time.sleep(settings.poll_seconds)
    raise CheckFailure(
        "Tempo",
        "fixture와 시나리오를 포함한 Auth trace 전체를 수집하지 못했습니다",
    )


def _loki_records(
    settings: Settings,
    start_ns: int,
    contains: str | None = None,
) -> list[dict[str, object]]:
    query = (
        f'{{compose_project="{settings.project}",service="auth-service",stream="stdout"}}'
    )
    if contains:
        query += f" |= {json.dumps(contains)}"
    now = time.time_ns()
    params = urlencode(
        {
            "query": query,
            "start": str(start_ns),
            "end": str(now + 5_000_000_000),
            "limit": "1000",
            "direction": "forward",
        }
    )
    result = _request(
        "GET",
        f"{settings.loki_url}/loki/api/v1/query_range?{params}",
        backend="Loki",
    )
    if result.status != 200:
        raise CheckFailure("Loki", "query_range API가 HTTP 200을 반환하지 않았습니다")
    payload = _json(result, "Loki")
    data = payload.get("data")
    streams = data.get("result") if isinstance(data, dict) else None
    if not isinstance(streams, list):
        raise CheckFailure("Loki", "query_range 응답 형식이 올바르지 않습니다")
    records: list[dict[str, object]] = []
    for stream in streams:
        if not isinstance(stream, dict):
            continue
        labels = stream.get("stream")
        if not isinstance(labels, dict) or labels.get("service") != "auth-service":
            continue
        values = stream.get("values")
        if not isinstance(values, list):
            continue
        for value in values:
            if not isinstance(value, list) or len(value) != 2 or not isinstance(value[1], str):
                continue
            try:
                decoded = json.loads(value[1])
            except json.JSONDecodeError:
                records.append({"_raw_log_line": value[1]})
                continue
            if isinstance(decoded, dict):
                records.append(decoded)
    return records


def _wait_access_log(
    settings: Settings,
    start_ns: int,
    evidence: TraceEvidence,
) -> dict[str, object]:
    scenario = evidence.scenario
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        records = _loki_records(settings, start_ns, scenario.request_id)
        matches = [
            record
            for record in records
            if record.get("msg") == "http.request.completed"
            and record.get("request_id") == scenario.request_id
        ]
        if matches:
            record = matches[-1]
            if record.get("service.name") != "auth-service":
                raise CheckFailure("Loki", f"{scenario.name} access log의 service.name이 다릅니다")
            if record.get("trace_id") != scenario.trace_id:
                raise CheckFailure("Loki", f"{scenario.name} access log의 trace ID가 다릅니다")
            if _normalize_id(record.get("span_id"), 8) != evidence.server_span.span_id:
                raise CheckFailure("Loki", f"{scenario.name} access log의 span ID가 다릅니다")
            if record.get("http.route") != ROUTE:
                raise CheckFailure("Loki", f"{scenario.name} access log의 route가 다릅니다")
            if record.get("http.status_code") != scenario.status:
                raise CheckFailure("Loki", f"{scenario.name} access log의 status가 다릅니다")
            return record
        time.sleep(settings.poll_seconds)
    raise CheckFailure("Loki", f"{scenario.name} request/trace 상관관계 access log가 없습니다")


def _assert_grafana_datasource(settings: Settings, uid: str, datasource_type: str) -> None:
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        try:
            result = _request(
                "GET",
                f"{settings.grafana_url}/api/datasources/uid/{uid}",
                backend="Grafana",
            )
            if result.status == 200:
                payload = _json(result, "Grafana")
                if payload.get("uid") == uid and payload.get("type") == datasource_type:
                    return
        except CheckFailure:
            pass
        time.sleep(settings.poll_seconds)
    raise CheckFailure("Grafana", f"{datasource_type} datasource가 provision되지 않았습니다")


def _assert_grafana_query(
    settings: Settings,
    *,
    ref_id: str,
    datasource_name: str,
    target: dict[str, object],
    start_ms: int,
    end_ms: int,
) -> None:
    request_payload = {
        "from": str(start_ms),
        "to": str(end_ms),
        "queries": [{"refId": ref_id, **target}],
    }
    deadline = time.monotonic() + settings.timeout_seconds
    while time.monotonic() < deadline:
        try:
            result = _request(
                "POST",
                f"{settings.grafana_url}/api/ds/query",
                payload=request_payload,
                backend="Grafana",
            )
            if result.status == 200:
                results = _json(result, "Grafana").get("results")
                query_result = results.get(ref_id) if isinstance(results, dict) else None
                if isinstance(query_result, dict):
                    status = query_result.get("status")
                    frames = query_result.get("frames")
                    if (
                        not query_result.get("error")
                        and status in (None, 200)
                        and isinstance(frames, list)
                        and frames
                    ):
                        return
        except CheckFailure:
            pass
        time.sleep(settings.poll_seconds)
    raise CheckFailure(
        "Grafana",
        f"{datasource_name} datasource query에 data frame이 없습니다",
    )


def _assert_grafana_queries(
    settings: Settings,
    start_ns: int,
    evidence: TraceEvidence,
) -> None:
    for uid, datasource_type in (
        ("tempo", "tempo"),
        ("prometheus", "prometheus"),
        ("loki", "loki"),
    ):
        _assert_grafana_datasource(settings, uid, datasource_type)

    start_ms = start_ns // 1_000_000
    end_ms = (time.time_ns() + 5_000_000_000) // 1_000_000
    _assert_grafana_query(
        settings,
        ref_id="T",
        datasource_name="Tempo",
        target={
            "datasource": {"type": "tempo", "uid": "tempo"},
            "queryType": "traceId",
            "query": evidence.scenario.trace_id,
        },
        start_ms=start_ms,
        end_ms=end_ms,
    )
    _assert_grafana_query(
        settings,
        ref_id="P",
        datasource_name="Prometheus",
        target={
            "datasource": {"type": "prometheus", "uid": "prometheus"},
            "expr": 'redis_up{job="redis-exporter"}',
            "format": "time_series",
            "instant": True,
            "range": False,
            "intervalMs": 1000,
            "maxDataPoints": 100,
        },
        start_ms=start_ms,
        end_ms=end_ms,
    )
    scenario = evidence.scenario
    _assert_grafana_query(
        settings,
        ref_id="L",
        datasource_name="Loki",
        target={
            "datasource": {"type": "loki", "uid": "loki"},
            "expr": (
                f'{{compose_project="{settings.project}",service="auth-service",stream="stdout"}} '
                f'| json | request_id="{scenario.request_id}" | trace_id="{scenario.trace_id}"'
            ),
            "queryType": "range",
            "direction": "backward",
            "maxLines": 100,
        },
        start_ms=start_ms,
        end_ms=end_ms,
    )


def _assert_sensitive_absent(
    traces: list[dict[str, object]],
    logs: list[dict[str, object]],
    fixture: Fixture,
    credentials: Credentials,
) -> None:
    serialized = json.dumps(
        {"traces": traces, "logs": logs},
        ensure_ascii=False,
        separators=(",", ":"),
    )
    exact_values = (
        fixture.email,
        fixture.password,
        fixture.user_id,
        credentials.access_token,
        credentials.refresh_token,
        credentials.session_id,
        credentials.flow_token,
        f"{SESSION_STATUS_KEY_PREFIX}{credentials.session_id}",
    )
    if any(value and value in serialized for value in exact_values):
        raise CheckFailure("보안", "trace 또는 log에 fixture 인증정보나 Redis key/value가 노출됐습니다")
    lowered = serialized.lower()
    forbidden_fragments = (
        "auth:session-status:",
        "-----begin private key-----",
        "\"authorization\"",
        "\"cookie\"",
        "bearer ",
        "rtk_",
    )
    if any(fragment in lowered for fragment in forbidden_fragments):
        raise CheckFailure("보안", "trace 또는 log에 인증 header, cookie, Redis key가 노출됐습니다")
    if JWT_PATTERN.search(serialized):
        raise CheckFailure("보안", "trace 또는 log에 JWT 형식 문자열이 노출됐습니다")
    if EMAIL_PATTERN.search(serialized):
        raise CheckFailure("보안", "trace 또는 log에 email 형식 문자열이 노출됐습니다")


def _assert_operational_spans_absent(traces: list[dict[str, object]]) -> None:
    for trace in traces:
        for span in _span_items(trace):
            route = span.attributes.get("http.route")
            if _is_server(span) and (
                route in OPERATIONAL_ROUTES
                or span.name in {f"GET {path}" for path in OPERATIONAL_ROUTES}
            ):
                raise CheckFailure("Tempo", "operational endpoint의 HTTP server span이 저장됐습니다")


def _run_smoke(settings: Settings) -> None:
    run_start_ns = time.time_ns() - 2_000_000_000
    print("[검증] 실제 Auth 로그인 fixture를 준비합니다", flush=True)
    fixture = _seed_fixture(settings)
    credentials = _issue_credentials(settings, fixture)
    _delete_session_cache(settings, credentials.session_id)

    print("[검증] operational endpoint의 HTTP trace 제외 정책을 확인합니다", flush=True)
    barrier_trace = _assert_operational_traces_excluded(settings, credentials.access_token)
    _delete_session_cache(settings, credentials.session_id, require_existing=True)
    _assert_prometheus_targets(settings)
    baseline_time = time.time()
    baseline = _metric_snapshot(settings, baseline_time)
    if baseline.redis_up != 1:
        raise CheckFailure("Prometheus", "시나리오 시작 전 redis_up이 1이 아닙니다")

    print("[검증] Redis cache miss와 PostgreSQL write-through를 확인합니다", flush=True)
    miss = _scenario_request(settings, "cache miss", credentials.access_token, 200)
    miss_trace = _validate_trace(settings, miss, "miss")
    miss_metrics = _wait_metrics(
        settings,
        time.time(),
        "cache miss/client nil/CAS ok/server miss metric 증가가 확인되지 않았습니다",
        lambda current: (
            current.client_nil > baseline.client_nil
            and current.client_ok > baseline.client_ok
            and current.keyspace_misses > baseline.keyspace_misses
            and current.commands > baseline.commands
        ),
    )

    print("[검증] Redis cache hit에 PostgreSQL 조회가 없는지 확인합니다", flush=True)
    hit = _scenario_request(settings, "cache hit", credentials.access_token, 200)
    hit_trace = _validate_trace(settings, hit, "hit")
    hit_metrics = _wait_metrics(
        settings,
        time.time(),
        "cache hit/client ok/server hit metric 증가가 확인되지 않았습니다",
        lambda current: (
            current.client_ok > miss_metrics.client_ok
            and current.keyspace_hits > miss_metrics.keyspace_hits
            and current.commands > miss_metrics.commands
        ),
    )

    print("[검증] Redis 장애 시 fail-closed와 readiness 실패를 확인합니다", flush=True)
    redis_stopped = False
    fault_trace: TraceEvidence
    fault: ScenarioResponse
    try:
        _compose(settings, "stop", "redis", condition="Redis 제어 중단에 실패했습니다")
        redis_stopped = True
        fault = _scenario_request(settings, "Redis 장애", credentials.access_token, 503)
        fault_trace = _validate_trace(settings, fault, "fault")
        readiness = _request(
            "GET",
            f"{settings.admin_url}/readyz",
            backend="Auth readiness",
        )
        if readiness.status != 503:
            raise CheckFailure("Auth readiness", "Redis 장애 중 /readyz가 HTTP 503이 아닙니다")
        _wait_outage_metrics(
            settings,
            time.time(),
            hit_metrics.client_error,
        )
    finally:
        if redis_stopped:
            _compose(
                settings,
                "start",
                "redis",
                condition="Redis 복구 시작에 실패했습니다",
            )

    print("[검증] Redis 복구 후 readiness와 server metric을 확인합니다", flush=True)
    _wait_status(settings, "Auth readiness", f"{settings.admin_url}/readyz")
    recovery_metrics = _wait_metrics(
        settings,
        time.time(),
        "Redis 복구 후 redis_up=1이 되지 않았습니다",
        lambda current: current.redis_up == 1,
    )
    _assert_auth_metric(settings)

    recovery = _scenario_request(settings, "Redis 복구", credentials.access_token, 200)
    recovery_trace = _validate_trace(settings, recovery, "hit")
    _wait_metrics(
        settings,
        time.time(),
        "Redis 복구 후 인증 요청의 client/server hit metric 증가가 없습니다",
        lambda current: (
            current.client_ok > recovery_metrics.client_ok
            and current.keyspace_hits > recovery_metrics.keyspace_hits
            and current.commands > recovery_metrics.commands
        ),
    )

    print("[검증] Redis 장애 중 Session 폐기와 worker 재시도를 확인합니다", flush=True)
    revoked = _verify_projection_retry(settings, credentials)
    revoked_trace = _validate_trace(settings, revoked, "hit")

    if len({miss.request_id, hit.request_id, fault.request_id, recovery.request_id, revoked.request_id}) != 5:
        raise CheckFailure("runner", "시나리오 request ID가 고유하지 않습니다")
    print("[검증] Loki access log의 request/trace/route/status 상관관계를 확인합니다", flush=True)
    scenario_traces = [miss_trace, hit_trace, fault_trace, recovery_trace, revoked_trace]
    access_logs = [
        _wait_access_log(settings, run_start_ns, evidence) for evidence in scenario_traces
    ]
    print("[검증] Grafana datasource API로 trace, metric, log 조회를 확인합니다", flush=True)
    _assert_grafana_queries(settings, run_start_ns, miss_trace)
    all_logs = _loki_records(settings, run_start_ns)
    all_traces = _all_auth_trace_payloads(
        settings,
        {
            *credentials.trace_ids,
            barrier_trace.scenario.trace_id,
            *(trace.scenario.trace_id for trace in scenario_traces),
        },
    )
    _assert_operational_spans_absent(all_traces)
    _assert_sensitive_absent(
        all_traces,
        [*all_logs, *access_logs],
        fixture,
        credentials,
    )
    print(
        "[성공] Tempo trace, Auth/Redis metric, Loki log, Grafana datasource API, 장애 복구, worker projection 재시도, 민감정보 비노출을 모두 확인했습니다",
        flush=True,
    )


def _up(settings: Settings) -> None:
    key_path = _ensure_key(settings)
    _cleanup(settings)
    print("[준비] Auth Redis 관측성 stack을 시작합니다", flush=True)
    _compose(
        settings,
        "up",
        "-d",
        "--build",
        condition="관측성 stack 시작에 실패했습니다",
    )
    _wait_stack(settings)
    _assert_prometheus_targets(settings)
    if key_path != _key_path(settings):
        raise CheckFailure("fixture", "RSA key lifecycle이 일치하지 않습니다")


def _print_stack_urls(settings: Settings) -> None:
    print(f"[유지] Auth: {settings.auth_url}")
    print(f"[유지] Auth admin: {settings.admin_url}")
    print(f"[유지] Tempo: {settings.tempo_url}")
    print(f"[유지] Prometheus: {settings.prometheus_url}")
    print(f"[유지] Loki: {settings.loki_url}")
    print(f"[유지] Grafana: {settings.grafana_url}")


def main() -> int:
    """Run, keep, verify, or remove the dedicated Auth observability stack."""
    settings = Settings.load()
    command = sys.argv[1] if len(sys.argv) == 2 else ""
    if command not in {"run", "up", "smoke", "down"}:
        raise CheckFailure("runner", "사용법: run.py run|up|smoke|down")
    if command == "down":
        try:
            _cleanup(settings)
        finally:
            _remove_generated_key(settings)
        print("[정리] 관측성 stack과 volume, 임시 RSA key를 제거했습니다")
        return 0
    if command == "up":
        _up(settings)
        _print_stack_urls(settings)
        return 0
    if command == "smoke":
        _wait_stack(settings)
        _run_smoke(settings)
        return 0

    failure: BaseException | None = None
    try:
        _up(settings)
        _run_smoke(settings)
    except BaseException as error:
        failure = error
    try:
        _cleanup(settings)
    except BaseException as error:
        if failure is None:
            failure = error
        else:
            print("[실패] backend=Docker condition=검증 실패 뒤 정리도 완료하지 못했습니다", file=sys.stderr)
    finally:
        try:
            _remove_generated_key(settings)
        except OSError as error:
            if failure is None:
                failure = error
            else:
                print(
                    "[실패] backend=fixture condition=검증 실패 뒤 임시 RSA key도 제거하지 못했습니다",
                    file=sys.stderr,
                )
    if failure is not None:
        if isinstance(
            failure,
            (CheckFailure, OSError, subprocess.SubprocessError, KeyboardInterrupt),
        ):
            raise failure
        raise CheckFailure("runner", "예상하지 못한 검증 오류가 발생했습니다") from failure
    print("[정리] 성공 후 모든 컨테이너, network, volume, 임시 RSA key를 제거했습니다")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except CheckFailure as error:
        print(
            f"[실패] backend={error.backend} condition={error.condition}",
            file=sys.stderr,
        )
        raise SystemExit(1) from None
    except (OSError, subprocess.SubprocessError, KeyboardInterrupt):
        print(
            "[실패] backend=runner condition=예상하지 못한 실행 오류가 발생했습니다",
            file=sys.stderr,
        )
        raise SystemExit(1) from None
    except Exception:
        print(
            "[실패] backend=runner condition=예상하지 못한 검증 오류가 발생했습니다",
            file=sys.stderr,
        )
        raise SystemExit(1) from None
