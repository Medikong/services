# noqa: SIZE_OK - local HTTP harness and fixture builders are one test seam.
from __future__ import annotations

import hashlib
import hmac
import json
import os
import subprocess
import tempfile
import threading
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Iterator, Literal

REPOSITORY_ROOT = Path(__file__).resolve().parents[4]
RUNNER = REPOSITORY_ROOT / "tests/e2e/scripts/run_aws_purchase_scenarios.py"
RUN_ID = "aws-purchase-20260724T120000Z-1234abcd"
DROP_ID = "opaque-drop-20260724"
PRODUCT_ID = "opaque-product-20260724"
ORDER_ID = "order-sensitive-001"
PAYMENT_ID = "payment-sensitive-001"
SAFE_JWT = (
    "eyJhbGciOiJSUzI1NiIsImtpZCI6InRlc3QifQ."
    "eyJzdWIiOiJzeW50aGV0aWMtY3VzdG9tZXIifQ."
    "c2lnbmF0dXJl"
)
ATTESTATION_KEY = b"test-only-scenario-attestation-key-material"
_RUNTIME_INPUTS = (
    "AWS_PURCHASE_INGRESS_BASE_URL",
    "AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT",
    "AWS_PURCHASE_ATTESTATION_KEY_FINGERPRINT",
    "AWS_PURCHASE_JWT",
    "SYNTHETIC_CUSTOMER_EMAIL",
    "SYNTHETIC_CUSTOMER_PASSWORD",
    "SYNTHETIC_CUSTOMER_B_EMAIL",
    "SYNTHETIC_CUSTOMER_B_PASSWORD",
)


@dataclass(frozen=True, slots=True)
class RecordedRequest:
    method: str
    path: str
    headers: tuple[tuple[str, str], ...]
    body: str

    def header(self, name: str) -> str:
        return dict(self.headers).get(name.lower(), "")


class ScenarioState:
    def __init__(
        self,
        payment_approval_statuses: tuple[int, ...] = (201,),
    ) -> None:
        self.requests: list[RecordedRequest] = []
        self.order_created = False
        self.payment_approved = False
        self.payment_approval_statuses = list(payment_approval_statuses)
        self.payment_approval_statuses_seen: list[int] = []
        self._lock = threading.Lock()

    def record(self, request: RecordedRequest) -> None:
        with self._lock:
            self.requests.append(request)


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
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length).decode("utf-8")
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

        def _reply(  # noqa: OBJECT_OK - test payloads intentionally untyped
            self,
            status: int,
            payload: dict[str, object],  # noqa: OBJECT_OK
        ) -> None:
            body = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Set-Cookie", "private-session-cookie")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self) -> None:
            self._record()
            authorization = self.headers.get("Authorization", "")
            if self.path == "/.well-known/jwks.json":
                self._reply(200, {"keys": []})
                return
            if self.path == "/notifications":
                if authorization != f"Bearer {SAFE_JWT}":
                    self._reply(401, {"detail": "unauthorized"})
                    return
                notifications = (
                    [
                        {
                            "id": "notification-sensitive-001",
                            "userId": "user-sensitive-001",
                            "orderId": ORDER_ID,
                            "type": "ORDER_CONFIRMED",
                            "title": "confirmed",
                            "message": "confirmed",
                            "createdAt": "2026-07-24T12:00:00Z",
                            "read": False,
                        }
                    ]
                    if state.payment_approved
                    else []
                )
                self._reply(
                    200,
                    {"data": notifications, "pageInfo": {"nextCursor": None}},
                )
                return
            if self.path == f"/drops/{DROP_ID}":
                remaining = 41 if state.order_created else 42
                self._reply(
                    200,
                    {
                        "data": {
                            "id": DROP_ID,
                            "status": "OPEN",
                            "products": [
                                {
                                    "id": PRODUCT_ID,
                                    "price": 50000,
                                    "remainingQuantity": remaining,
                                    "inventoryVersion": 1,
                                }
                            ],
                        }
                    },
                )
                return
            if self.path == f"/orders/{ORDER_ID}":
                self._reply(200, {"data": _order_payload(state)})
                return
            if self.path == f"/payments/{PAYMENT_ID}":
                self._reply(200, {"data": _payment_payload()})
                return
            self._reply(404, {})

        def do_POST(self) -> None:
            self._record()
            if self.path == "/api/v1/auth/intents":
                self._reply(
                    201,
                    {
                        "data": {
                            "authIntentId": "intent-sensitive",
                            "authFlowToken": "flow-sensitive",
                        }
                    },
                )
                return
            if self.path == "/api/v1/auth/signins/email":
                self._reply(
                    200,
                    {"data": {"tokens": {"accessToken": SAFE_JWT}}},
                )
                return
            if self.path == "/orders":
                state.order_created = True
                self._reply(201, {"data": _order_payload(state)})
                return
            if self.path == "/payments/mock-approvals":
                status = (
                    state.payment_approval_statuses.pop(0)
                    if state.payment_approval_statuses
                    else 201
                )
                state.payment_approval_statuses_seen.append(status)
                if status == 201:
                    state.payment_approved = True
                    self._reply(status, {"data": _payment_payload()})
                else:
                    self._reply(
                        status,
                        {"detail": "order is not ready for payment"},
                    )
                return
            self._reply(404, {})

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    host, port = server.server_address
    try:
        yield ServerHarness(base_url=f"http://{host}:{port}", state=state)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def _order_payload(  # noqa: OBJECT_OK - test payloads intentionally untyped
    state: ScenarioState,
) -> dict[str, object]:  # noqa: OBJECT_OK
    return {
        "id": ORDER_ID,
        "userId": "user-sensitive-001",
        "dropId": DROP_ID,
        "productId": PRODUCT_ID,
        "quantity": 1,
        "amount": 50000,
        "status": "CONFIRMED" if state.payment_approved else "PENDING_PAYMENT",
        "createdAt": "2026-07-24T12:00:00Z",
        "paymentId": PAYMENT_ID if state.payment_approved else None,
    }


def _payment_payload() -> dict[str, object]:  # noqa: OBJECT_OK
    return {
        "id": PAYMENT_ID,
        "orderId": ORDER_ID,
        "userId": "user-sensitive-001",
        "amount": 50000,
        "method": "MOCK_CARD",
        "status": "APPROVED",
        "createdAt": "2026-07-24T12:00:01Z",
        "approvedAt": "2026-07-24T12:00:01Z",
    }


@dataclass(frozen=True, slots=True)
class Invocation:
    result: subprocess.CompletedProcess[str]
    output_path: Path

    def report(self) -> dict[str, object]:  # noqa: OBJECT_OK
        return json.loads(self.output_path.read_text(encoding="utf-8"))

    def emitted(self) -> str:
        artifact = (
            self.output_path.read_text(encoding="utf-8")
            if self.output_path.is_file()
            else ""
        )
        return "\n".join((self.result.stdout, self.result.stderr, artifact))


def invoke_runner(
    tmp_path: Path,
    *,
    base_url: str,
    mode: str,
    scenario: str = "04",
    runtime_env: dict[str, str] | None = None,
    live_attestation: Literal["valid", "stale-replay", "forged"] | None = None,
    extra_args: tuple[str, ...] = (),
    precreate_output: bool = False,
) -> Invocation:
    fixture_path = tmp_path / "fixture.json"
    fixture_path.write_text(json.dumps(_fixture_manifest()), encoding="utf-8")
    output_path = tmp_path / "result.json"
    if precreate_output:
        output_path.write_text("stale", encoding="utf-8")
    command = [
        "uv",
        "run",
        "--script",
        str(RUNNER),
        "--environment",
        "aws-dev",
        "--mode",
        mode,
        "--scenario",
        scenario,
        "--base-url",
        base_url,
        "--run-id",
        RUN_ID,
        "--fixture-manifest",
        str(fixture_path),
        "--json-output",
        str(output_path),
        "--max-attempts",
        "1",
        "--poll-attempts",
        "2",
        "--poll-interval-seconds",
        "0",
        "--timeout-seconds",
        "1",
    ]
    if live_attestation is not None:
        attestation_path = tmp_path / "live-attestation.json"
        issued_at = datetime.now(UTC).replace(microsecond=0)
        if live_attestation == "stale-replay":
            issued_at -= timedelta(minutes=10)
        signing_key = (
            b"caller-authored-forged-attestation-key"
            if live_attestation == "forged"
            else ATTESTATION_KEY
        )
        attestation_path.write_text(
            json.dumps(_live_attestation(issued_at, signing_key)),
            encoding="utf-8",
        )
        attestation_key_path = tmp_path / "attestation.key"
        attestation_key_path.write_bytes(signing_key)
        command.extend(
            (
                "--live-fixture-attestation",
                str(attestation_path),
                "--attestation-key-file",
                str(attestation_key_path),
                "--write-opt-in",
                f"aws-dev:{RUN_ID}:ALLOW_PURCHASE_WRITES",
            )
        )
    command.extend(extra_args)
    environment = os.environ.copy()
    environment["UV_CACHE_DIR"] = str(
        Path(tempfile.gettempdir())
        / "medikong-aws-purchase-scenario-uv-cache"
    )
    environment.pop("UV_OFFLINE", None)
    for name in _RUNTIME_INPUTS:
        environment.pop(name, None)
    environment.update(
        runtime_env
        or {
            "SYNTHETIC_CUSTOMER_EMAIL": "customer-a-secret-email",
            "SYNTHETIC_CUSTOMER_PASSWORD": "customer-a-secret-password",
        }
    )
    digest = hashlib.sha256(base_url.encode("utf-8")).hexdigest()
    environment.setdefault(
        "AWS_PURCHASE_EXPECTED_INGRESS_FINGERPRINT",
        f"sha256:{digest}",
    )
    environment["AWS_PURCHASE_ATTESTATION_KEY_FINGERPRINT"] = (
        f"sha256:{hashlib.sha256(ATTESTATION_KEY).hexdigest()}"
    )
    result = subprocess.run(
        command,
        cwd=REPOSITORY_ROOT,
        env=environment,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        timeout=45,
    )
    return Invocation(result=result, output_path=output_path)


def _fixture_manifest() -> dict[str, object]:  # noqa: OBJECT_OK
    return {
        "schema_version": 1,
        "run_id": RUN_ID,
        "users": [
            {
                "subject_ref": "opaque-user-a",
                "credential_ref": "opaque-credential-a",
                "credential_status": "present",
                "role": "customer",
            },
            {
                "subject_ref": "opaque-user-b",
                "credential_ref": "opaque-credential-b",
                "credential_status": "present",
                "role": "customer",
            },
        ],
        "fixture": {
            "namespace": RUN_ID,
            "drop_ref": DROP_ID,
            "product_ref": PRODUCT_ID,
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


def _live_attestation(
    issued_at: datetime,
    signing_key: bytes,
) -> dict[str, object]:  # noqa: OBJECT_OK
    fingerprint = lambda value: hashlib.sha256(value.encode("utf-8")).hexdigest()[
        :16
    ]
    artifact: dict[str, object] = {
        "schema_version": 1,
        "environment": "aws-dev",
        "run_id": RUN_ID,
        "verdict": "LIVE_FIXTURE_VERIFIED",
        "api_traffic_allowed": True,
        "collector": "medikong.aws-live-fixture-attestation/v1",
        "issued_at": issued_at.isoformat().replace("+00:00", "Z"),
        "users": {
            "count": 2,
            "subject_fingerprints": [
                fingerprint("opaque-user-a"),
                fingerprint("opaque-user-b"),
            ],
            "credential_bindings": "VERIFIED",
        },
        "fixture": {
            "drop_fingerprint": fingerprint(DROP_ID),
            "product_fingerprint": fingerprint(PRODUCT_ID),
            "dedicated": True,
            "stock": 42,
            "active_records": 0,
        },
    }
    artifact["integrity"] = "hmac-sha256:" + hmac.new(
        signing_key,
        json.dumps(
            artifact,
            separators=(",", ":"),
            sort_keys=True,
        ).encode(),
        hashlib.sha256,
    ).hexdigest()
    return artifact
