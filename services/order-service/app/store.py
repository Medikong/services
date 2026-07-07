from dataclasses import dataclass
from datetime import UTC, datetime
from typing import assert_never

from contracts import PaymentApprovedEvent, PaymentFailedEvent

from app.catalog import PRODUCT_CATALOG, ProductForSale, product_for
from app.models import (
    DropId,
    IdempotencyKey,
    Order,
    OrderId,
    OrderStatus,
    PaymentId,
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


type CreateOrderResult = OrderCreated | OrderAlreadyCreated | OrderIdempotencyConflict | ProductUnavailable


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


type PaymentApprovalResult = PaymentApplied | PaymentAlreadyApplied | PaymentEventOrderMissing | PaymentIgnored


@dataclass(frozen=True, slots=True)
class PaymentFailureApplied:
    order: Order


@dataclass(frozen=True, slots=True)
class PaymentFailureAlreadyApplied:
    order: Order


type PaymentFailureResult = (
    PaymentFailureApplied | PaymentFailureAlreadyApplied | PaymentEventOrderMissing | PaymentIgnored
)


class OrderStore:
    def __init__(self, catalog: tuple[ProductForSale, ...] = PRODUCT_CATALOG) -> None:
        self._catalog = catalog
        self._orders: dict[OrderId, Order] = {}
        self._idempotency_index: dict[tuple[UserId, IdempotencyKey], IdempotencyEntry] = {}
        self._reserved_quantities: dict[tuple[DropId, ProductId], int] = {}
        self._next_order_number = 1

    async def create_order(self, command: CreateOrderCommand) -> CreateOrderResult:
        fingerprint = order_request_fingerprint(command)
        replayed_order = self._replayed_order(command.user_id, command.idempotency_key)
        if replayed_order is not None:
            if not order_matches_command(replayed_order, command):
                return OrderIdempotencyConflict(order=replayed_order)
            return OrderAlreadyCreated(order=replayed_order)

        product = product_for(self._catalog, command.drop_id, command.product_id)
        if product is None or command.quantity > self._available_quantity(product):
            return ProductUnavailable(
                drop_id=command.drop_id,
                product_id=command.product_id,
            )

        order_id = self._next_order_id()
        order = Order(
            id=order_id,
            userId=command.user_id,
            dropId=command.drop_id,
            productId=command.product_id,
            quantity=command.quantity,
            amount=product.unit_price * command.quantity,
            status=OrderStatus.PENDING_PAYMENT,
            createdAt=datetime.now(UTC),
        )
        self._orders[order_id] = order
        self._reserved_quantities[(command.drop_id, command.product_id)] = (
            self._reserved_quantity(product) + command.quantity
        )
        self._idempotency_index[(command.user_id, command.idempotency_key)] = IdempotencyEntry(
            order_id=order_id,
            fingerprint=fingerprint,
        )
        return OrderCreated(order=order, idempotency_key=command.idempotency_key)

    async def get_order(self, order_id: OrderId) -> Order | None:
        return self._orders.get(order_id)

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult:
        order_id = OrderId(event.orderId)
        order = self._orders.get(order_id)
        if order is None:
            return PaymentEventOrderMissing(order_id=order_id)

        match order.status:
            case OrderStatus.PENDING_PAYMENT:
                confirmed_order = order.model_copy(
                    update={
                        "status": OrderStatus.CONFIRMED,
                        "paymentId": PaymentId(event.paymentId),
                        "confirmedAt": event.occurredAt,
                    },
                )
                self._orders[order_id] = confirmed_order
                return PaymentApplied(order=confirmed_order)
            case OrderStatus.CONFIRMED:
                return PaymentAlreadyApplied(order=order)
            case OrderStatus.PAYMENT_FAILED | OrderStatus.CANCELED | OrderStatus.EXPIRED:
                return PaymentIgnored(order=order)
            case unreachable:
                assert_never(unreachable)

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult:
        order_id = OrderId(event.orderId)
        order = self._orders.get(order_id)
        if order is None:
            return PaymentEventOrderMissing(order_id=order_id)

        match order.status:
            case OrderStatus.PENDING_PAYMENT:
                self._release_reserved_quantity(order)
                failed_order = order.model_copy(
                    update={
                        "status": OrderStatus.PAYMENT_FAILED,
                        "paymentId": PaymentId(event.paymentId),
                    },
                )
                self._orders[order_id] = failed_order
                return PaymentFailureApplied(order=failed_order)
            case OrderStatus.PAYMENT_FAILED:
                return PaymentFailureAlreadyApplied(order=order)
            case OrderStatus.CONFIRMED | OrderStatus.CANCELED | OrderStatus.EXPIRED:
                return PaymentIgnored(order=order)
            case unreachable:
                assert_never(unreachable)

    def _replayed_order(
        self,
        user_id: UserId,
        idempotency_key: IdempotencyKey,
    ) -> Order | None:
        entry = self._idempotency_index.get((user_id, idempotency_key))
        if entry is None:
            return None
        return self._orders[entry.order_id]

    def _next_order_id(self) -> OrderId:
        order_id = OrderId(f"order-{self._next_order_number:03d}")
        self._next_order_number += 1
        return order_id

    def _available_quantity(self, product: ProductForSale) -> int:
        return product.remaining_quantity - self._reserved_quantity(product)

    def _reserved_quantity(self, product: ProductForSale) -> int:
        return self._reserved_quantities.get((product.drop_id, product.product_id), 0)

    def _release_reserved_quantity(self, order: Order) -> None:
        key = (DropId(order.dropId), ProductId(order.productId))
        reserved_quantity = self._reserved_quantities.get(key, 0)
        remaining_quantity = reserved_quantity - order.quantity
        if remaining_quantity <= 0:
            self._reserved_quantities.pop(key, None)
            return
        self._reserved_quantities[key] = remaining_quantity


def order_request_fingerprint(command: CreateOrderCommand) -> OrderRequestFingerprint:
    return OrderRequestFingerprint(
        drop_id=command.drop_id,
        product_id=command.product_id,
        quantity=command.quantity,
    )


def order_matches_command(order: Order, command: CreateOrderCommand) -> bool:
    return (
        order.userId == command.user_id
        and order.dropId == command.drop_id
        and order.productId == command.product_id
        and order.quantity == command.quantity
    )
