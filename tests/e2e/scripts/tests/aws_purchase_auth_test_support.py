from __future__ import annotations

import hashlib
import json
import os
import subprocess
import tempfile
import threading
from contextlib import contextmanager
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Iterator, TypedDict, cast

REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
RUNNER = REPOSITORY_ROOT / "tests/e2e/scripts/run_aws_purchase_auth.py"
RUN_ID = "aws-purchase-20260723T150000Z-1234abcd"
_EXPECTED_INGRESS_FINGERPRINT = "AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT"
SAFE_JWT = (
    "eyJhbGciOiJSUzI1NiIsImtpZCI6InRlc3QifQ."
    "eyJzdWIiOiJzeW50aGV0aWMtY3VzdG9tZXIifQ."
    "c2lnbmF0dXJl"
)
_RUNTIME_INPUTS = (
    "AWS_PURCHASE_INGRESS_BASE_URL",
    _EXPECTED_INGRESS_FINGERPRINT,
    "AWS_PURCHASE_JWT",
    "SYNTHETIC_CUSTOMER_EMAIL",
    "SYNTHETIC_CUSTOMER_PASSWORD",
)
type JsonValue = (
    str | int | float | bool | None | list[JsonValue] | dict[str, JsonValue]
)


class StagePayload(TypedDict):
    attempts: int


class ReportPayload(TypedDict):
    verdict: str
    reason_code: str
    purchase_traffic_allowed: bool
    purchase_requests_sent: int
    stages: list[StagePayload]


@dataclass(frozen=True, slots=True)
class RecordedRequest:
    method: str
    path: str
    headers: tuple[tuple[str, str], ...]
    body: str

    def header(self, name: str) -> str:
        normalized = name.lower()
        return dict(self.headers).get(normalized, "")


class ScenarioState:
    def __init__(
        self,
        *,
        auth_statuses: tuple[int, ...] = (200,),
        anonymous_status: int = 401,
        authenticated_status: int = 200,
        login_token: str | None = SAFE_JWT,
        dropped_auth_connections: int = 0,
        redirect_location: str = "",
        response_marker: str = "",
    ) -> None:
        self.auth_statuses = auth_statuses
        self.anonymous_status = anonymous_status
        self.authenticated_status = authenticated_status
        self.login_token = login_token
        self.dropped_auth_connections = dropped_auth_connections
        self.redirect_location = redirect_location
        self.response_marker = response_marker
        self.requests: list[RecordedRequest] = []
        self._auth_attempts = 0
        self._lock = threading.Lock()

    def record(self, request: RecordedRequest) -> None:
        with self._lock:
            self.requests.append(request)

    def next_auth_status(self) -> int:
        with self._lock:
            index = min(self._auth_attempts, len(self.auth_statuses) - 1)
            self._auth_attempts += 1
            return self.auth_statuses[index]

    def should_drop_auth(self) -> bool:
        with self._lock:
            if self.dropped_auth_connections <= 0:
                return False
            self.dropped_auth_connections -= 1
            return True


@dataclass(frozen=True, slots=True)
class ServerHarness:
    base_url: str
    state: ScenarioState


@contextmanager
def run_server(state: ScenarioState) -> Iterator[ServerHarness]:
    class Handler(BaseHTTPRequestHandler):
        def log_message(
            self,
            format: str,
            *args: str | int | float | bool | None,
        ) -> None:
            del format, args

        def _record(self) -> None:
            content_length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(content_length).decode("utf-8")
            state.record(
                RecordedRequest(
                    method=self.command,
                    path=self.path,
                    headers=tuple(
                        sorted(
                            (name.lower(), value)
                            for name, value in self.headers.items()
                        )
                    ),
                    body=body,
                )
            )

        def _reply(self, status: int, payload: dict[str, JsonValue]) -> None:
            body = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            if state.redirect_location:
                self.send_header("Location", state.redirect_location)
            if state.response_marker:
                self.send_header("Set-Cookie", state.response_marker)
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self) -> None:
            self._record()
            if self.path == "/.well-known/jwks.json":
                if state.should_drop_auth():
                    self.close_connection = True
                    return
                self._reply(
                    state.next_auth_status(),
                    {"keys": [], "marker": state.response_marker},
                )
                return
            if self.path == "/v1/users/me/interests":
                authorization = self.headers.get("Authorization", "")
                status = (
                    state.authenticated_status
                    if authorization == f"Bearer {SAFE_JWT}"
                    else state.anonymous_status
                    if not authorization
                    else 401
                )
                self._reply(status, {"marker": state.response_marker})
                return
            self._reply(404, {})

        def do_POST(self) -> None:
            self._record()
            if self.path == "/api/v1/auth/intents":
                self._reply(
                    201,
                    {
                        "data": {
                            "authIntentId": "aint_test",
                            "authFlowToken": "flow-token",
                        }
                    },
                )
                return
            if self.path == "/api/v1/auth/signins/email":
                tokens: dict[str, JsonValue] = {}
                if state.login_token is not None:
                    tokens["accessToken"] = state.login_token
                self._reply(200, {"data": {"tokens": tokens}})
                return
            self._reply(404, {})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    host, port = cast("tuple[str, int]", server.server_address)
    try:
        yield ServerHarness(base_url=f"http://{host}:{port}", state=state)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


@dataclass(frozen=True, slots=True)
class RunnerInvocation:
    result: subprocess.CompletedProcess[str]
    json_path: Path
    junit_path: Path

    def report(self) -> ReportPayload:
        return cast(
            "ReportPayload",
            json.loads(self.json_path.read_text(encoding="utf-8")),
        )

    def emitted(self) -> str:
        files = [
            path.read_text(encoding="utf-8")
            for path in (self.json_path, self.junit_path)
            if path.is_file()
        ]
        return "\n".join((self.result.stdout, self.result.stderr, *files))


def invoke_runner(
    tmp_path: Path,
    *,
    base_url: str | None,
    runtime_env: dict[str, str] | None = None,
    extra_args: tuple[str, ...] = (),
    precreate_json: bool = False,
) -> RunnerInvocation:
    json_path = tmp_path / "result.json"
    junit_path = tmp_path / "result.junit.xml"
    if precreate_json:
        json_path.write_text("stale", encoding="utf-8")
    command = [
        "uv",
        "run",
        "--script",
        str(RUNNER),
        "--run-id",
        RUN_ID,
        "--json-output",
        str(json_path),
        "--junit-output",
        str(junit_path),
        "--max-attempts",
        "1",
        "--backoff-seconds",
        "0",
        "--timeout-seconds",
        "1",
    ]
    if base_url is not None:
        command.extend(("--base-url", base_url))
    command.extend(extra_args)
    environment = os.environ.copy()
    environment.setdefault(
        "UV_CACHE_DIR",
        str(Path(tempfile.gettempdir()) / "medikong-aws-purchase-auth-uv-cache"),
    )
    for name in _RUNTIME_INPUTS:
        environment.pop(name, None)
    inputs = {"AWS_PURCHASE_JWT": SAFE_JWT} if runtime_env is None else runtime_env
    environment.update(inputs)
    if approved_url := base_url or environment.get("AWS_PURCHASE_INGRESS_BASE_URL"):
        digest = hashlib.sha256(approved_url.encode()).hexdigest()
        environment.setdefault(_EXPECTED_INGRESS_FINGERPRINT, f"sha256:{digest}")
    result = subprocess.run(
        command,
        cwd=REPOSITORY_ROOT,
        env=environment,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=30,
    )
    return RunnerInvocation(result=result, json_path=json_path, junit_path=junit_path)
