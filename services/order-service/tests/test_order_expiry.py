from datetime import UTC, datetime, timedelta

import anyio
import pytest

from app.expiry import OrderExpiryWorker
from app.order_config import (
    DEFAULT_ORDER_PAYMENT_TTL_SECONDS,
    OrderPaymentTtlConfigurationError,
    order_payment_policy_from_env,
)

NOW = datetime(2026, 7, 15, 1, 2, 3, tzinfo=UTC)


class RecordingExpirer:
    def __init__(self, outcomes: list[bool]) -> None:
        self._outcomes: list[bool] = outcomes
        self.calls: list[datetime] = []

    async def expire_due_order(self, now: datetime) -> bool:
        self.calls.append(now)
        return self._outcomes.pop(0)


def test_payment_ttl_defaults_to_five_minutes() -> None:
    # Given
    env: dict[str, str] = {}

    # When
    policy = order_payment_policy_from_env(env)

    # Then
    assert policy.ttl == timedelta(seconds=DEFAULT_ORDER_PAYMENT_TTL_SECONDS)
    assert DEFAULT_ORDER_PAYMENT_TTL_SECONDS == 300


def test_payment_ttl_uses_configured_positive_seconds() -> None:
    # Given
    env = {"ORDER_PAYMENT_TTL_SECONDS": "17"}

    # When
    policy = order_payment_policy_from_env(env)

    # Then
    assert policy.ttl == timedelta(seconds=17)


def test_payment_ttl_accepts_operational_maximum() -> None:
    policy = order_payment_policy_from_env({"ORDER_PAYMENT_TTL_SECONDS": "86400"})

    assert policy.ttl == timedelta(seconds=86400)


@pytest.mark.parametrize(
    "value",
    (
        "0",
        "-1",
        "invalid",
        "86401",
        "9" * 1000,
    ),
)
def test_payment_ttl_rejects_non_positive_or_invalid_values(value: str) -> None:
    # Given
    env = {"ORDER_PAYMENT_TTL_SECONDS": value}

    # When / Then
    with pytest.raises(OrderPaymentTtlConfigurationError):
        order_payment_policy_from_env(env)


def test_expiry_worker_uses_injected_clock() -> None:
    # Given
    repository = RecordingExpirer([True])
    worker = OrderExpiryWorker(repository, lambda: NOW)

    # When
    processed = anyio.run(worker.process_once)

    # Then
    assert processed is True
    assert repository.calls == [NOW]
