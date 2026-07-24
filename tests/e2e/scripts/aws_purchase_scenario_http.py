# noqa: SIZE_OK - cohesive bounded HTTP adapter; splitting transport and
# response-contract helpers would add import ceremony without reducing the
# scenario boundary's readability.
from __future__ import annotations

import socket
import time
from dataclasses import dataclass
from types import TracebackType
from typing import Final

import httpx2
from pydantic import TypeAdapter, ValidationError

from aws_purchase_auth_contract import is_jwt_shaped
from aws_purchase_scenario_models import (
    BearerToken,
    Config,
    Credentials,
    Fixture,
    JsonObject,
    JsonValue,
    RunnerStop,
    Stage,
    Verdict,
)

_JSON_ADAPTER: Final = TypeAdapter(JsonValue)
_LIMITS: Final = httpx2.Limits(
    max_connections=200,
    max_keepalive_connections=40,
    keepalive_expiry=30,
)
_SOCKET_OPTIONS: Final = [(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)]


@dataclass(frozen=True, slots=True)
class RequestPlan:
    stage: str
    method: str
    path: str
    token: BearerToken | None = None
    payload: JsonObject | None = None
    idempotency_suffix: str | None = None
    auth_flow_token: str | None = None
    mobile_auth: bool = False
    purchase_write: bool = False


@dataclass(frozen=True, slots=True)
class Receipt:
    response: httpx2.Response
    attempts: int


@dataclass(frozen=True, slots=True)
class CatalogProduct:
    price: int
    remaining_quantity: int


class ScenarioHttpClient:
    def __init__(self, config: Config) -> None:
        self._config = config
        timeout = httpx2.Timeout(
            connect=config.bounds.timeout_seconds,
            read=config.bounds.timeout_seconds,
            write=config.bounds.timeout_seconds,
            pool=config.bounds.timeout_seconds,
        )
        transport = httpx2.HTTPTransport(
            http2=True,
            retries=0,
            limits=_LIMITS,
            socket_options=_SOCKET_OPTIONS,
        )
        self._client = httpx2.Client(
            base_url=config.base_url,
            transport=transport,
            timeout=timeout,
            follow_redirects=False,
            trust_env=False,
            headers={"Accept": "application/json"},
        )
        self._stages: list[Stage] = []
        self._requests_sent = 0
        self._purchase_writes_sent = 0

    @property
    def stages(self) -> tuple[Stage, ...]:
        return tuple(self._stages)

    @property
    def requests_sent(self) -> int:
        return self._requests_sent

    @property
    def purchase_writes_sent(self) -> int:
        return self._purchase_writes_sent

    def __enter__(self) -> ScenarioHttpClient:
        self._client.__enter__()
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self._client.__exit__(exception_type, exception, traceback)

    def request(self, plan: RequestPlan) -> Receipt:
        idempotency_key = (
            f"{self._config.run_id}-{plan.idempotency_suffix}"
            if plan.idempotency_suffix is not None
            else None
        )
        for attempt in range(1, self._config.bounds.max_attempts + 1):
            headers = self._headers(plan, attempt, idempotency_key)
            self._requests_sent += 1
            if plan.purchase_write:
                self._purchase_writes_sent += 1
            try:
                response = self._client.request(
                    plan.method,
                    plan.path,
                    headers=headers,
                    json=plan.payload,
                )
            except httpx2.TransportError:
                if attempt == self._config.bounds.max_attempts:
                    self._stages.append(
                        Stage(plan.stage, plan.method, 0, attempt)
                    )
                    raise RunnerStop(
                        Verdict.BLOCKED,
                        "INGRESS_UNREACHABLE",
                    ) from None
                time.sleep(0.1 * attempt)
                continue
            if response.status_code < 500 or (
                attempt == self._config.bounds.max_attempts
            ):
                self._stages.append(
                    Stage(plan.stage, plan.method, response.status_code, attempt)
                )
                return Receipt(response=response, attempts=attempt)
            time.sleep(0.1 * attempt)
        raise AssertionError("bounded HTTP request exhausted")

    def authenticate(self, credentials: Credentials) -> BearerToken:
        jwks = self.request(
            RequestPlan("jwks", "GET", "/.well-known/jwks.json")
        )
        if jwks.response.status_code != 200:
            reason = (
                "AUTH_ROUTE_UNRESOLVED"
                if jwks.response.status_code == 404
                or 300 <= jwks.response.status_code < 400
                else "AUTH_ROUTE_UNAVAILABLE"
            )
            raise RunnerStop(Verdict.BLOCKED, reason)
        keys = member(decode_json(jwks), ("keys",))
        if type(keys) is not list:
            raise RunnerStop(Verdict.BLOCKED, "AUTH_ROUTE_INVALID")
        intent = self.request(
            RequestPlan(
                stage="auth_intent",
                method="POST",
                path="/api/v1/auth/intents",
                payload={
                    "returnPath": "/",
                    "intentType": "navigation",
                },
                mobile_auth=True,
            )
        )
        _require_auth_status(intent, 201)
        intent_id = member(decode_json(intent), ("data", "authIntentId"))
        flow_token = member(
            decode_json(intent),
            ("data", "authFlowToken"),
        )
        if type(intent_id) is not str or type(flow_token) is not str:
            raise RunnerStop(Verdict.FAIL, "AUTH_RESPONSE_INVALID")
        sign_in = self.request(
            RequestPlan(
                stage="email_sign_in",
                method="POST",
                path="/api/v1/auth/signins/email",
                payload={
                    "authIntentId": intent_id,
                    "email": credentials.email,
                    "password": credentials.password,
                    "rememberMe": False,
                },
                auth_flow_token=flow_token,
                mobile_auth=True,
            )
        )
        _require_auth_status(sign_in, 200)
        token = member(
            decode_json(sign_in),
            ("data", "tokens", "accessToken"),
        )
        if type(token) is not str:
            raise RunnerStop(Verdict.FAIL, "TOKEN_MISSING")
        if not is_jwt_shaped(token):
            raise RunnerStop(Verdict.FAIL, "TOKEN_INVALID_FORMAT")
        return BearerToken(token)

    def preflight(self, token: BearerToken, fixture: Fixture) -> CatalogProduct:
        anonymous = self.request(
            RequestPlan("notifications_anonymous", "GET", "/notifications")
        )
        if anonymous.response.status_code not in {401, 403}:
            reason = (
                "PROTECTED_ROUTE_OPEN"
                if 200 <= anonymous.response.status_code < 300
                else "PROTECTED_ROUTE_UNRESOLVED"
                if anonymous.response.status_code == 404
                or 300 <= anonymous.response.status_code < 400
                else "PROTECTED_ROUTE_UNAVAILABLE"
            )
            raise RunnerStop(Verdict.FAIL, reason)
        authenticated = self.request(
            RequestPlan(
                "notifications_authenticated",
                "GET",
                "/notifications",
                token=token,
            )
        )
        if authenticated.response.status_code != 200:
            verdict = (
                Verdict.FAIL
                if authenticated.response.status_code in {401, 403}
                else Verdict.BLOCKED
            )
            raise RunnerStop(verdict, "PROTECTED_ROUTE_UNAVAILABLE")
        drop = self.request(
            RequestPlan(
                "fixture_catalog",
                "GET",
                f"/drops/{fixture.drop_id}",
                token=token,
            )
        )
        if drop.response.status_code != 200:
            raise RunnerStop(Verdict.BLOCKED, "FIXTURE_NOT_FOUND")
        return catalog_product(drop, fixture, fixture.initial_stock)

    def _headers(
        self,
        plan: RequestPlan,
        attempt: int,
        idempotency_key: str | None,
    ) -> dict[str, str]:
        headers = {
            "X-Request-Id": (
                f"{self._config.run_id}-{plan.stage}-{attempt}"
            ),
        }
        if plan.token is not None:
            headers["Authorization"] = f"Bearer {plan.token}"
        if idempotency_key is not None:
            headers["Idempotency-Key"] = idempotency_key
        if plan.mobile_auth:
            headers["X-Client-Channel"] = "ios"
            headers["X-Device-Installation-Id"] = (
                f"{self._config.run_id}-device"
            )
        if plan.auth_flow_token is not None:
            headers["X-Auth-Flow-Token"] = plan.auth_flow_token
        return headers


def decode_json(receipt: Receipt) -> JsonValue:
    try:
        return _JSON_ADAPTER.validate_json(receipt.response.content)
    except ValidationError as error:
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID") from error


def member(value: JsonValue, path: tuple[str, ...]) -> JsonValue:
    current = value
    for name in path:
        if type(current) is not dict or name not in current:
            raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
        current = current[name]
    return current


def mapping(value: JsonValue) -> JsonObject:
    if type(value) is not dict:
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
    return value


def _require_auth_status(receipt: Receipt, expected: int) -> None:
    status = receipt.response.status_code
    if status == expected:
        return
    if status in {401, 403}:
        raise RunnerStop(Verdict.FAIL, "CREDENTIALS_REJECTED")
    if status == 404 or 300 <= status < 400:
        raise RunnerStop(Verdict.BLOCKED, "AUTH_ROUTE_UNRESOLVED")
    raise RunnerStop(Verdict.BLOCKED, "AUTH_ROUTE_UNAVAILABLE")


def catalog_product(
    receipt: Receipt,
    fixture: Fixture,
    expected_remaining: int,
) -> CatalogProduct:
    data = mapping(member(decode_json(receipt), ("data",)))
    if data.get("id") != fixture.drop_id or data.get("status") != "OPEN":
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_NOT_READY")
    products = data.get("products")
    if type(products) is not list:
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
    matching = [
        product
        for product in products
        if type(product) is dict and product.get("id") == fixture.product_id
    ]
    if len(matching) != 1:
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_NOT_READY")
    product = matching[0]
    price = product.get("price")
    remaining = product.get("remainingQuantity")
    if type(price) is not int or type(remaining) is not int:
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
    if remaining != expected_remaining:
        raise RunnerStop(Verdict.BLOCKED, "FIXTURE_STOCK_MISMATCH")
    return CatalogProduct(price=price, remaining_quantity=remaining)
