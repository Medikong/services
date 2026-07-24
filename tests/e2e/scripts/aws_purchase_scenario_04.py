# noqa: SIZE_OK
from __future__ import annotations

import time
from dataclasses import dataclass

from aws_purchase_scenario_http import (
    CatalogProduct,
    Receipt,
    RequestPlan,
    ScenarioHttpClient,
    catalog_product,
    decode_json,
    mapping,
    member,
)
from aws_purchase_scenario_models import (
    BearerToken,
    Config,
    RunnerStop,
    ScenarioSummary,
    Verdict,
    fingerprint,
)


@dataclass(frozen=True, slots=True)
class _Order:
    identifier: str
    amount: int
    status: str
    payment_id: str | None


@dataclass(frozen=True, slots=True)
class _Payment:
    identifier: str
    order_id: str
    amount: int
    status: str


def execute_happy_path(
    client: ScenarioHttpClient,
    config: Config,
    token: BearerToken,
    catalog: CatalogProduct,
) -> ScenarioSummary:
    create_plan = RequestPlan(
        stage="order_create",
        method="POST",
        path="/orders",
        token=token,
        payload={
            "dropId": config.fixture.drop_id,
            "productId": config.fixture.product_id,
            "quantity": 1,
        },
        idempotency_suffix="04-order",
        purchase_write=True,
    )
    created = _parse_order(
        _require_status(client.request(create_plan), 201),
        config,
    )
    if created.status != "PENDING_PAYMENT" or created.amount != catalog.price:
        raise RunnerStop(Verdict.FAIL, "ORDER_CREATE_CONTRACT_FAILED")
    replayed = _parse_order(
        _require_status(
            client.request(
                RequestPlan(
                    stage="order_create_replay",
                    method=create_plan.method,
                    path=create_plan.path,
                    token=token,
                    payload=create_plan.payload,
                    idempotency_suffix=create_plan.idempotency_suffix,
                    purchase_write=True,
                )
            ),
            201,
        ),
        config,
    )
    if replayed != created:
        raise RunnerStop(Verdict.FAIL, "ORDER_IDEMPOTENCY_FAILED")
    payment_plan = RequestPlan(
        stage="payment_approve",
        method="POST",
        path="/payments/mock-approvals",
        token=token,
        payload={
            "orderId": created.identifier,
            "amount": created.amount,
            "method": "MOCK_CARD",
        },
        idempotency_suffix="04-payment",
        purchase_write=True,
    )
    approved = _approve_payment(client, config, payment_plan)
    _verify_payment(approved, created)
    replayed_payment = _parse_payment(
        _require_status(
            client.request(
                RequestPlan(
                    stage="payment_approve_replay",
                    method=payment_plan.method,
                    path=payment_plan.path,
                    token=token,
                    payload=payment_plan.payload,
                    idempotency_suffix=payment_plan.idempotency_suffix,
                    purchase_write=True,
                )
            ),
            201,
        )
    )
    if replayed_payment != approved:
        raise RunnerStop(Verdict.FAIL, "PAYMENT_IDEMPOTENCY_FAILED")
    confirmed = _poll_confirmed_order(client, config, token, created)
    payment_read = _parse_payment(
        _require_status(
            client.request(
                RequestPlan(
                    "payment_get",
                    "GET",
                    f"/payments/{approved.identifier}",
                    token=token,
                )
            ),
            200,
        )
    )
    if payment_read != approved:
        raise RunnerStop(Verdict.FAIL, "PAYMENT_READ_CONTRACT_FAILED")
    notification_count = _verify_stable_notification(
        client,
        config,
        token,
        confirmed.identifier,
    )
    final_catalog = catalog_product(
        _require_status(
            client.request(
                RequestPlan(
                    "fixture_catalog_after",
                    "GET",
                    f"/drops/{config.fixture.drop_id}",
                    token=token,
                )
            ),
            200,
        ),
        config.fixture,
        config.fixture.initial_stock - 1,
    )
    return ScenarioSummary(
        order_status=confirmed.status,
        payment_status=payment_read.status,
        notification_count=notification_count,
        inventory_delta=(
            config.fixture.initial_stock - final_catalog.remaining_quantity
        ),
        order_fingerprint=fingerprint(confirmed.identifier),
        payment_fingerprint=fingerprint(payment_read.identifier),
    )


def _poll_confirmed_order(
    client: ScenarioHttpClient,
    config: Config,
    token: BearerToken,
    created: _Order,
) -> _Order:
    for _ in range(config.bounds.poll_attempts):
        current = _parse_order(
            _require_status(
                client.request(
                    RequestPlan(
                        "order_poll",
                        "GET",
                        f"/orders/{created.identifier}",
                        token=token,
                    )
                ),
                200,
            ),
            config,
        )
        if current.status == "CONFIRMED":
            if (
                current.identifier != created.identifier
                or current.amount != created.amount
                or current.payment_id is None
            ):
                raise RunnerStop(
                    Verdict.FAIL,
                    "ORDER_CONFIRMATION_CONTRACT_FAILED",
                )
            return current
        if current.status != "PENDING_PAYMENT":
            raise RunnerStop(Verdict.FAIL, "ORDER_TERMINAL_STATE_INVALID")
        if config.bounds.poll_interval_seconds > 0:
            time.sleep(config.bounds.poll_interval_seconds)
    raise RunnerStop(Verdict.FAIL, "ORDER_CONFIRMATION_TIMEOUT")


def _approve_payment(
    client: ScenarioHttpClient,
    config: Config,
    payment_plan: RequestPlan,
) -> _Payment:
    for attempt in range(config.bounds.max_attempts):
        receipt = client.request(payment_plan)
        if receipt.response.status_code == 201:
            return _parse_payment(receipt)
        if receipt.response.status_code != 409:
            _require_status(receipt, 201)
        if attempt + 1 == config.bounds.max_attempts:
            raise RunnerStop(Verdict.FAIL, "HTTP_CONTRACT_FAILED")
        if config.bounds.poll_interval_seconds > 0:
            time.sleep(config.bounds.poll_interval_seconds)
    raise AssertionError("bounded payment approval retry exhausted")


def _verify_stable_notification(
    client: ScenarioHttpClient,
    config: Config,
    token: BearerToken,
    order_id: str,
) -> int:
    observed: list[int] = []
    for _ in range(2):
        receipt = _require_status(
            client.request(
                RequestPlan(
                    "notification_stability",
                    "GET",
                    "/notifications",
                    token=token,
                )
            ),
            200,
        )
        data = member(decode_json(receipt), ("data",))
        if type(data) is not list:
            raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
        count = sum(
            1
            for item in data
            if type(item) is dict
            and item.get("orderId") == order_id
            and item.get("type") == "ORDER_CONFIRMED"
        )
        observed.append(count)
        if config.bounds.poll_interval_seconds > 0:
            time.sleep(config.bounds.poll_interval_seconds)
    if observed != [1, 1]:
        raise RunnerStop(Verdict.FAIL, "NOTIFICATION_CARDINALITY_FAILED")
    return observed[-1]


def _parse_order(receipt: Receipt, config: Config) -> _Order:
    data = mapping(member(decode_json(receipt), ("data",)))
    identifier = data.get("id")
    amount = data.get("amount")
    status = data.get("status")
    payment_id = data.get("paymentId")
    if (
        type(identifier) is not str
        or type(amount) is not int
        or type(status) is not str
        or payment_id is not None
        and type(payment_id) is not str
        or data.get("dropId") != config.fixture.drop_id
        or data.get("productId") != config.fixture.product_id
        or data.get("quantity") != 1
    ):
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
    return _Order(identifier, amount, status, payment_id)


def _parse_payment(receipt: Receipt) -> _Payment:
    data = mapping(member(decode_json(receipt), ("data",)))
    identifier = data.get("id")
    order_id = data.get("orderId")
    amount = data.get("amount")
    status = data.get("status")
    if (
        type(identifier) is not str
        or type(order_id) is not str
        or type(amount) is not int
        or type(status) is not str
    ):
        raise RunnerStop(Verdict.FAIL, "RESPONSE_CONTRACT_INVALID")
    return _Payment(identifier, order_id, amount, status)


def _verify_payment(payment: _Payment, order: _Order) -> None:
    if (
        payment.order_id != order.identifier
        or payment.amount != order.amount
        or payment.status != "APPROVED"
    ):
        raise RunnerStop(Verdict.FAIL, "PAYMENT_APPROVAL_CONTRACT_FAILED")


def _require_status(receipt: Receipt, expected: int) -> Receipt:
    if receipt.response.status_code != expected:
        raise RunnerStop(Verdict.FAIL, "HTTP_CONTRACT_FAILED")
    return receipt
