from datetime import UTC, datetime
from typing import assert_never

from contracts import (
    PaymentApprovedEvent,
    PaymentFailedEvent,
    RefundCompletedEvent,
    RefundFailedEvent,
)

from app.catalog import PRODUCTS_FOR_SALE, ProductForSale, product_for
from app.cancellations import (
    InMemoryCancellationEntry,
    InMemoryRefundState,
    RequestCancellationCommand,
    RequestCancellationResult,
    apply_in_memory_refund_completed,
    apply_in_memory_refund_failed,
    request_in_memory_cancellation,
)
from app.models import (
    Cancellation,
    DropId,
    IdempotencyKey,
    Order,
    OrderId,
    OrderStatus,
    PaymentId,
    ProductId,
    UserId,
)
from app.store_contracts import (
    DEFAULT_IN_MEMORY_INVENTORY,
    CreateOrderCommand,
    CreateOrderResult,
    IdempotencyEntry,
    InventorySeed,
    OrderAlreadyCreated,
    OrderCreated,
    OrderIdempotencyConflict,
    OrderRequestFingerprint,
    PaymentAlreadyApplied,
    PaymentApplied,
    PaymentApprovalResult,
    PaymentEventOrderMissing,
    PaymentFailureAlreadyApplied,
    PaymentFailureApplied,
    PaymentFailureResult,
    PaymentIgnored,
    ProductSoldOut,
    ProductUnavailable,
)


class OrderStore:
    def __init__(
        self,
        catalog: tuple[ProductForSale, ...] = PRODUCTS_FOR_SALE,
        inventory: tuple[InventorySeed, ...] = DEFAULT_IN_MEMORY_INVENTORY,
    ) -> None:
        self._catalog = catalog
        self._total_quantities = {
            (item.drop_id, item.product_id): item.total_quantity for item in inventory
        }
        self._orders: dict[OrderId, Order] = {}
        self._cancellations: dict[OrderId, InMemoryCancellationEntry] = {}
        self._processed_refund_event_ids: set[str] = set()
        self._idempotency_index: dict[
            tuple[UserId, IdempotencyKey], IdempotencyEntry
        ] = {}
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
        if product is None:
            return ProductUnavailable(
                drop_id=command.drop_id,
                product_id=command.product_id,
            )
        if command.quantity > self._available_quantity(product):
            return ProductSoldOut(
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
        self._idempotency_index[(command.user_id, command.idempotency_key)] = (
            IdempotencyEntry(
                order_id=order_id,
                fingerprint=fingerprint,
            )
        )
        return OrderCreated(order=order, idempotency_key=command.idempotency_key)

    async def get_order(self, order_id: OrderId) -> Order | None:
        return self._orders.get(order_id)

    async def request_cancellation(
        self,
        command: RequestCancellationCommand,
    ) -> RequestCancellationResult:
        return request_in_memory_cancellation(
            self._orders,
            self._cancellations,
            command,
        )

    async def get_cancellation(
        self,
        order_id: OrderId,
        user_id: UserId,
    ) -> Cancellation | None:
        entry = self._cancellations.get(order_id)
        if entry is None or entry.user_id != user_id:
            return None
        return entry.cancellation

    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool:
        applied = apply_in_memory_refund_completed(
            InMemoryRefundState(
                self._orders,
                self._cancellations,
                self._processed_refund_event_ids,
            ),
            event,
        )
        if applied:
            self._release_reserved_quantity(self._orders[OrderId(event.orderId)])
        return applied

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool:
        return apply_in_memory_refund_failed(
            InMemoryRefundState(
                self._orders,
                self._cancellations,
                self._processed_refund_event_ids,
            ),
            event,
        )

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
            case (
                OrderStatus.PAYMENT_FAILED
                | OrderStatus.CANCEL_PENDING
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
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
            case (
                OrderStatus.CONFIRMED
                | OrderStatus.CANCEL_PENDING
                | OrderStatus.CANCELED
                | OrderStatus.EXPIRED
            ):
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
        total_quantity = self._total_quantities.get(
            (product.drop_id, product.product_id), 0
        )
        return total_quantity - self._reserved_quantity(product)

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
