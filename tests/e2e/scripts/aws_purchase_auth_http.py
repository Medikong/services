from __future__ import annotations

from dataclasses import dataclass
from typing import Never

from aws_purchase_auth_contract import (
    Credential,
    JsonValue,
    LoginCredential,
    RunnerConfig,
    StageRecord,
    TokenCredential,
    Verdict,
    is_jwt_shaped,
)
from aws_purchase_auth_transport import (
    BoundedHttpClient,
    IngressUnreachable,
    RequestReceipt,
    decode_json,
    first_string,
    json_member,
)


@dataclass(frozen=True, slots=True)
class ProbeResult:
    verdict: Verdict
    reason_code: str
    stages: tuple[StageRecord, ...]


class ProbeStop(Exception):
    def __init__(self, verdict: Verdict, reason_code: str) -> None:
        super().__init__(reason_code)
        self.verdict = verdict
        self.reason_code = reason_code


class AuthProbe:
    def __init__(self, config: RunnerConfig) -> None:
        self._config = config
        self._stages: list[StageRecord] = []

    def run(self, credential: Credential) -> ProbeResult:
        try:
            with BoundedHttpClient(self._config) as client:
                self._verify_jwks(client)
                token = (
                    credential.token
                    if isinstance(credential, TokenCredential)
                    else self._login(client, credential)
                )
                self._verify_anonymous_boundary(client)
                self._verify_authenticated_boundary(client, token)
        except ProbeStop as stop:
            return ProbeResult(
                verdict=stop.verdict,
                reason_code=stop.reason_code,
                stages=tuple(self._stages),
            )
        return ProbeResult(
            verdict=Verdict.VERIFIED,
            reason_code="AUTH_PREFLIGHT_VERIFIED",
            stages=tuple(self._stages),
        )

    def _verify_jwks(self, client: BoundedHttpClient) -> None:
        receipt = self._request(client, "jwks", "GET", self._config.auth_route)
        response = receipt.response
        if response.status_code == 200:
            keys = json_member(decode_json(response), ("keys",))
            if isinstance(keys, list):
                self._record("jwks", "PASSED", receipt)
                return
            self._block("jwks", receipt, "AUTH_ROUTE_INVALID")
        if response.status_code == 404 or 300 <= response.status_code < 400:
            self._block("jwks", receipt, "AUTH_ROUTE_UNRESOLVED")
        self._block("jwks", receipt, "AUTH_ROUTE_UNAVAILABLE")

    def _login(
        self,
        client: BoundedHttpClient,
        credential: LoginCredential,
    ) -> str:
        intent = self._request(
            client,
            "auth_intent",
            "POST",
            "/api/v1/auth/intents",
            payload={"returnPath": "/", "intentType": "navigation"},
            headers={
                "X-Client-Channel": "mobile",
                "X-Device-Installation-Id": f"{self._config.run_id}-device",
            },
        )
        if not 200 <= intent.response.status_code < 300:
            self._halt_login("auth_intent", intent)
        intent_id = json_member(
            decode_json(intent.response),
            ("data", "authIntentId"),
        )
        flow_token = json_member(
            decode_json(intent.response),
            ("data", "authFlowToken"),
        )
        if not isinstance(intent_id, str) or not isinstance(flow_token, str):
            self._fail("auth_intent", intent, "AUTH_RESPONSE_INVALID")
        self._record("auth_intent", "PASSED", intent)
        sign_in = self._request(
            client,
            "email_sign_in",
            "POST",
            "/api/v1/auth/signins/email",
            payload={
                "authIntentId": intent_id,
                "email": credential.email,
                "password": credential.password,
                "rememberMe": False,
            },
            headers={
                "X-Auth-Flow-Token": flow_token,
                "X-Client-Channel": "mobile",
                "X-Device-Installation-Id": f"{self._config.run_id}-device",
            },
        )
        if not 200 <= sign_in.response.status_code < 300:
            self._halt_login("email_sign_in", sign_in)
        decoded = decode_json(sign_in.response)
        token = first_string(
            json_member(decoded, ("data", "tokens", "accessToken")),
            json_member(decoded, ("data", "access", "accessToken")),
        )
        if token is None:
            self._fail("email_sign_in", sign_in, "TOKEN_MISSING")
        if not is_jwt_shaped(token):
            self._fail("email_sign_in", sign_in, "TOKEN_INVALID_FORMAT")
        self._record("email_sign_in", "PASSED", sign_in)
        return token

    def _verify_anonymous_boundary(self, client: BoundedHttpClient) -> None:
        receipt = self._request(
            client,
            "protected_anonymous",
            "GET",
            self._config.protected_route,
        )
        status = receipt.response.status_code
        if status in {401, 403}:
            self._record("protected_anonymous", "PASSED", receipt)
            return
        if 200 <= status < 300:
            self._fail("protected_anonymous", receipt, "PROTECTED_ROUTE_OPEN")
        if status == 404 or 300 <= status < 400:
            self._block("protected_anonymous", receipt, "PROTECTED_ROUTE_UNRESOLVED")
        self._block("protected_anonymous", receipt, "PROTECTED_ROUTE_UNAVAILABLE")

    def _verify_authenticated_boundary(
        self,
        client: BoundedHttpClient,
        token: str,
    ) -> None:
        receipt = self._request(
            client,
            "protected_authenticated",
            "GET",
            self._config.protected_route,
            headers={"Authorization": f"Bearer {token}"},
        )
        status = receipt.response.status_code
        if 200 <= status < 300:
            self._record("protected_authenticated", "PASSED", receipt)
            return
        if status in {401, 403}:
            self._fail("protected_authenticated", receipt, "TOKEN_REJECTED")
        if status == 404 or 300 <= status < 400:
            self._block(
                "protected_authenticated",
                receipt,
                "PROTECTED_ROUTE_UNRESOLVED",
            )
        self._block(
            "protected_authenticated",
            receipt,
            "PROTECTED_ROUTE_UNAVAILABLE",
        )

    def _request(
        self,
        client: BoundedHttpClient,
        stage: str,
        method: str,
        path: str,
        *,
        payload: dict[str, JsonValue] | None = None,
        headers: dict[str, str] | None = None,
    ) -> RequestReceipt:
        try:
            return client.request(
                stage,
                method,
                path,
                payload=payload,
                headers=headers,
            )
        except IngressUnreachable as stop:
            self._stages.append(
                StageRecord(
                    name=stage,
                    status="BLOCKED",
                    attempts=stop.attempts,
                    request_id=stop.request_id,
                    idempotency_key=stop.idempotency_key,
                )
            )
            raise ProbeStop(Verdict.BLOCKED, "INGRESS_UNREACHABLE") from None

    def _halt_login(self, stage: str, receipt: RequestReceipt) -> None:
        status = receipt.response.status_code
        if status == 404 or 300 <= status < 400:
            self._block(stage, receipt, "AUTH_ROUTE_UNRESOLVED")
        if status in {401, 403}:
            self._fail(stage, receipt, "CREDENTIALS_REJECTED")
        self._block(stage, receipt, "AUTH_ROUTE_UNAVAILABLE")

    def _block(
        self,
        stage: str,
        receipt: RequestReceipt,
        reason_code: str,
    ) -> Never:
        self._halt(stage, receipt, Verdict.BLOCKED, reason_code)

    def _fail(
        self,
        stage: str,
        receipt: RequestReceipt,
        reason_code: str,
    ) -> Never:
        self._halt(stage, receipt, Verdict.FAIL, reason_code)

    def _halt(
        self,
        stage: str,
        receipt: RequestReceipt,
        verdict: Verdict,
        reason_code: str,
    ) -> Never:
        self._record(stage, verdict.value, receipt)
        raise ProbeStop(verdict, reason_code)

    def _record(
        self,
        stage: str,
        status: str,
        receipt: RequestReceipt,
    ) -> None:
        self._stages.append(
            StageRecord(
                name=stage,
                status=status,
                attempts=receipt.attempts,
                request_id=receipt.request_id,
                idempotency_key=receipt.idempotency_key,
            )
        )
