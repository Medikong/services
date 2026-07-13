from __future__ import annotations

import json
import os
from dataclasses import dataclass
from typing import Final, NotRequired, Protocol, TypedDict
from urllib.error import HTTPError
from urllib.request import Request, urlopen

from contracts import PaymentFailedEvent
from pydantic import BaseModel, ConfigDict, ValidationError

DROP_ID: Final = "drop-001"
PRODUCT_ID: Final = "product-001"
PAYMENT_METHOD: Final = "MOCK_CARD"
PAYMENT_REASON: Final = "card_declined"
PAYMENT_NOT_READY_DETAIL: Final = "order is not ready for payment"
REQUEST_TIMEOUT_SECONDS: Final = 30
ORDER_POLL_INTERVAL_SECONDS: Final = 1
KAFKA_DRAIN_TIMEOUT_SECONDS: Final = 2
PAYMENT_READY_RETRY_INTERVAL_SECONDS: Final = 1


class OrderData(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    userId: str
    amount: int
    status: str
    paymentId: str | None = None


class OrderResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: OrderData


class PaymentData(BaseModel):
    model_config = ConfigDict(frozen=True)

    id: str
    orderId: str
    userId: str
    amount: int
    status: str
    failureReason: str | None = None


class PaymentResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: PaymentData


class ErrorResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    detail: str


class SmokeOutput(TypedDict):
    captured_payment_failed_copies: int
    error: NotRequired[str]
    event_ids: list[str]
    ok: bool
    order_id: str
    order_status: str
    payment_id: str
    payment_ids_match: bool
    run_id: str
    unique_event_ids: list[str]
    user_id: str


class KafkaMessage(Protocol):
    key: bytes | None
    value: bytes | None


@dataclass(frozen=True, slots=True)
class ScenarioIds:
    run_id: str
    user_id: str
    order_idempotency_key: str
    payment_idempotency_key: str


@dataclass(frozen=True, slots=True)
class ScenarioConfig:
    bootstrap_servers: str
    order_service_url: str
    payment_service_url: str
    ids: ScenarioIds
    timeout_seconds: int


@dataclass(frozen=True, slots=True)
class HttpResult:
    status: int
    body: bytes


@dataclass(frozen=True, slots=True)
class CapturedPaymentFailed:
    event: PaymentFailedEvent
    key: bytes | None
    value: bytes


@dataclass(frozen=True, slots=True)
class ExpectedPaymentFailed:
    user_id: str
    order_id: str
    payment_id: str


@dataclass(frozen=True, slots=True)
class SmokeFailure(Exception):
    message: str

    def __str__(self) -> str:
        return self.message


def scenario_ids(run_id: str) -> ScenarioIds:
    return ScenarioIds(
        run_id=run_id,
        user_id=f"{run_id}-user",
        order_idempotency_key=f"{run_id}-order",
        payment_idempotency_key=f"{run_id}-payment-failure",
    )


def post_json(url: str, payload: bytes, ids: ScenarioIds, idempotency_key: str) -> HttpResult:
    request = Request(
        url,
        data=payload,
        headers={
            "Content-Type": "application/json",
            "X-Request-Id": f"{idempotency_key}-request",
            "X-User-Id": ids.user_id,
            "X-User-Role": "CUSTOMER",
            "Idempotency-Key": idempotency_key,
        },
        method="POST",
    )
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS) as response:
            return HttpResult(status=response.status, body=response.read())
    except HTTPError as exc:
        return HttpResult(status=exc.code, body=exc.read())


def get_json(url: str, ids: ScenarioIds) -> HttpResult:
    request = Request(
        url,
        headers={
            "X-Request-Id": f"{ids.run_id}-get",
            "X-User-Id": ids.user_id,
            "X-User-Role": "CUSTOMER",
        },
        method="GET",
    )
    try:
        with urlopen(request, timeout=REQUEST_TIMEOUT_SECONDS) as response:
            return HttpResult(status=response.status, body=response.read())
    except HTTPError as exc:
        return HttpResult(status=exc.code, body=exc.read())


def post_order(config: ScenarioConfig) -> HttpResult:
    payload = json.dumps(
        {"dropId": DROP_ID, "productId": PRODUCT_ID, "quantity": 1},
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return post_json(
        f"{config.order_service_url.rstrip('/')}/orders",
        payload,
        config.ids,
        config.ids.order_idempotency_key,
    )


def post_payment_failure(config: ScenarioConfig, order: OrderData) -> HttpResult:
    payload = json.dumps(
        {
            "amount": order.amount,
            "method": PAYMENT_METHOD,
            "orderId": order.id,
            "reason": PAYMENT_REASON,
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return post_json(
        f"{config.payment_service_url.rstrip('/')}/payments/mock-failures",
        payload,
        config.ids,
        config.ids.payment_idempotency_key,
    )


def select_order(config: ScenarioConfig, order_id: str) -> HttpResult:
    return get_json(
        f"{config.order_service_url.rstrip('/')}/orders/{order_id}",
        config.ids,
    )


def is_payment_not_ready_conflict(result: HttpResult) -> bool:
    if result.status != 409:
        return False
    try:
        error = ErrorResponse.model_validate_json(result.body)
    except ValidationError:
        return False
    return error.detail == PAYMENT_NOT_READY_DETAIL


def payment_from_result(result: HttpResult) -> PaymentData:
    if result.status != 201:
        raise SmokeFailure(f"payment failure returned {result.status}: {result.body!r}")
    payment = PaymentResponse.model_validate_json(result.body).data
    if payment.status != "FAILED":
        raise SmokeFailure(f"payment status mismatch: {payment.status}")
    if payment.failureReason != PAYMENT_REASON:
        raise SmokeFailure(f"payment failure reason mismatch: {payment.failureReason}")
    return payment


def captured_event(
    message: KafkaMessage,
    expected: ExpectedPaymentFailed,
) -> CapturedPaymentFailed | None:
    value = message.value
    if value is None:
        return None
    event = PaymentFailedEvent.model_validate_json(value)
    if (
        event.userId != expected.user_id
        or event.orderId != expected.order_id
        or event.paymentId != expected.payment_id
    ):
        return None
    return CapturedPaymentFailed(event=event, key=message.key, value=value)


def config_from_env() -> ScenarioConfig:
    run_id = os.environ.get(
        "PAYMENT_FAILURE_IDEMPOTENCY_RUN_ID",
        "payment-failure-idempotency",
    )
    return ScenarioConfig(
        bootstrap_servers=os.environ.get("KAFKA_BOOTSTRAP_SERVERS", "kafka:29092"),
        order_service_url=os.environ.get("ORDER_SERVICE_URL", "http://order-service:8082"),
        payment_service_url=os.environ.get(
            "PAYMENT_SERVICE_URL",
            "http://payment-service:8083",
        ),
        ids=scenario_ids(run_id),
        timeout_seconds=int(
            os.environ.get("PAYMENT_FAILURE_IDEMPOTENCY_TIMEOUT_SECONDS", "60"),
        ),
    )


def failure_output(config: ScenarioConfig, error: str) -> SmokeOutput:
    return {
        "captured_payment_failed_copies": 0,
        "error": error,
        "event_ids": [],
        "ok": False,
        "order_id": "",
        "order_status": "",
        "payment_id": "",
        "payment_ids_match": False,
        "run_id": config.ids.run_id,
        "unique_event_ids": [],
        "user_id": config.ids.user_id,
    }
