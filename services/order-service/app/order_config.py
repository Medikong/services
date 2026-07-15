import os
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import timedelta
from typing import Final

from app.expiry import Clock, utc_now

ORDER_PAYMENT_TTL_SECONDS_ENV: Final = "ORDER_PAYMENT_TTL_SECONDS"
DEFAULT_ORDER_PAYMENT_TTL_SECONDS: Final = 300
MAX_ORDER_PAYMENT_TTL_SECONDS: Final = 86_400


@dataclass(frozen=True, slots=True)
class OrderPaymentTtlConfigurationError(RuntimeError):
    value: str

    def __str__(self) -> str:
        return (
            "ORDER_PAYMENT_TTL_SECONDS must be an integer between 1 and "
            f"{MAX_ORDER_PAYMENT_TTL_SECONDS}, got {self.value!r}"
        )


@dataclass(frozen=True, slots=True)
class OrderPaymentPolicy:
    ttl: timedelta = timedelta(seconds=DEFAULT_ORDER_PAYMENT_TTL_SECONDS)
    clock: Clock = utc_now


def order_payment_policy_from_env(
    env: Mapping[str, str] | None = None,
) -> OrderPaymentPolicy:
    source = os.environ if env is None else env
    raw_seconds = source.get(
        ORDER_PAYMENT_TTL_SECONDS_ENV,
        str(DEFAULT_ORDER_PAYMENT_TTL_SECONDS),
    )
    try:
        seconds = int(raw_seconds)
    except (OverflowError, ValueError) as error:
        raise OrderPaymentTtlConfigurationError(raw_seconds) from error
    if not 1 <= seconds <= MAX_ORDER_PAYMENT_TTL_SECONDS:
        raise OrderPaymentTtlConfigurationError(raw_seconds)
    try:
        ttl = timedelta(seconds=seconds)
    except OverflowError as error:
        raise OrderPaymentTtlConfigurationError(raw_seconds) from error
    return OrderPaymentPolicy(ttl=ttl)
