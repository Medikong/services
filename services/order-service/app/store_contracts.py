from dataclasses import dataclass
from typing import Final

from app.models import (
    DropId,
    IdempotencyKey,
    Order,
    OrderId,
    ProductId,
    UserId,
)


@dataclass(frozen=True, slots=True)
class CreateOrderCommand:
    user_id: UserId
    drop_id: DropId
    product_id: ProductId
    quantity: int
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class OrderCreated:
    order: Order
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class OrderAlreadyCreated:
    order: Order


@dataclass(frozen=True, slots=True)
class OrderIdempotencyConflict:
    order: Order


@dataclass(frozen=True, slots=True)
class ProductUnavailable:
    drop_id: DropId
    product_id: ProductId


@dataclass(frozen=True, slots=True)
class ProductSoldOut:
    drop_id: DropId
    product_id: ProductId


type CreateOrderResult = (
    OrderCreated
    | OrderAlreadyCreated
    | OrderIdempotencyConflict
    | ProductUnavailable
    | ProductSoldOut
)


@dataclass(frozen=True, slots=True)
class OrderRequestFingerprint:
    drop_id: DropId
    product_id: ProductId
    quantity: int


@dataclass(frozen=True, slots=True)
class IdempotencyEntry:
    order_id: OrderId
    fingerprint: OrderRequestFingerprint


@dataclass(frozen=True, slots=True)
class InventorySeed:
    drop_id: DropId
    product_id: ProductId
    total_quantity: int


DEFAULT_IN_MEMORY_INVENTORY: Final = (
    InventorySeed(DropId("drop-001"), ProductId("product-001"), 42),
    InventorySeed(DropId("drop-sold-out-001"), ProductId("product-sold-out-001"), 42),
)


@dataclass(frozen=True, slots=True)
class PaymentApplied:
    order: Order


@dataclass(frozen=True, slots=True)
class PaymentAlreadyApplied:
    order: Order


@dataclass(frozen=True, slots=True)
class PaymentEventOrderMissing:
    order_id: OrderId


@dataclass(frozen=True, slots=True)
class PaymentIgnored:
    order: Order


type PaymentApprovalResult = (
    PaymentApplied | PaymentAlreadyApplied | PaymentEventOrderMissing | PaymentIgnored
)


@dataclass(frozen=True, slots=True)
class PaymentFailureApplied:
    order: Order


@dataclass(frozen=True, slots=True)
class PaymentFailureAlreadyApplied:
    order: Order


type PaymentFailureResult = (
    PaymentFailureApplied
    | PaymentFailureAlreadyApplied
    | PaymentEventOrderMissing
    | PaymentIgnored
)
