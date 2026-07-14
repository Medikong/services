from dataclasses import dataclass
from datetime import UTC, datetime
from typing import assert_never
from uuid import uuid4

from contracts import (
    FulfillmentStatus,
    RefundCompletedEvent,
    RefundFailedEvent,
    RefundStatus,
)

from app.models import Cancellation, IdempotencyKey, Order, OrderId, OrderStatus, UserId
from app.refund_validation import refund_event_is_valid


@dataclass(frozen=True, slots=True)
class RequestCancellationCommand:
    order_id: OrderId
    user_id: UserId
    idempotency_key: IdempotencyKey
    reason: str


@dataclass(frozen=True, slots=True)
class CancellationRequested:
    cancellation: Cancellation


@dataclass(frozen=True, slots=True)
class CancellationAlreadyRequested:
    cancellation: Cancellation


@dataclass(frozen=True, slots=True)
class CancellationIdempotencyConflict:
    cancellation: Cancellation


@dataclass(frozen=True, slots=True)
class CancellationOrderMissing:
    order_id: OrderId


@dataclass(frozen=True, slots=True)
class CancellationOwnerMismatch:
    order_id: OrderId


@dataclass(frozen=True, slots=True)
class CancellationNotAllowed:
    order: Order


type RequestCancellationResult = (
    CancellationRequested
    | CancellationAlreadyRequested
    | CancellationIdempotencyConflict
    | CancellationOrderMissing
    | CancellationOwnerMismatch
    | CancellationNotAllowed
)


@dataclass(frozen=True, slots=True)
class InMemoryCancellationEntry:
    cancellation: Cancellation
    user_id: UserId
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class InMemoryRefundState:
    orders: dict[OrderId, Order]
    cancellations: dict[OrderId, InMemoryCancellationEntry]
    processed_event_ids: set[str]


def request_in_memory_cancellation(
    orders: dict[OrderId, Order],
    cancellations: dict[OrderId, InMemoryCancellationEntry],
    command: RequestCancellationCommand,
) -> RequestCancellationResult:
    order = orders.get(command.order_id)
    if order is None:
        return CancellationOrderMissing(order_id=command.order_id)
    if order.userId != command.user_id:
        return CancellationOwnerMismatch(order_id=command.order_id)
    existing = cancellations.get(command.order_id)
    if existing is None:
        existing = next(
            (
                entry
                for entry in cancellations.values()
                if entry.user_id == command.user_id
                and entry.idempotency_key == command.idempotency_key
            ),
            None,
        )
    if existing is not None:
        if (
            existing.cancellation.orderId == command.order_id
            and existing.user_id == command.user_id
            and existing.idempotency_key == command.idempotency_key
            and existing.cancellation.reason == command.reason
        ):
            return CancellationAlreadyRequested(
                cancellation=existing.cancellation.model_copy(
                    update={
                        "orderStatus": OrderStatus.CANCEL_PENDING,
                        "refundStatus": RefundStatus.REQUESTED,
                        "completedAt": None,
                    },
                ),
            )
        return CancellationIdempotencyConflict(cancellation=existing.cancellation)
    match order.status:
        case OrderStatus.CONFIRMED:
            match order.fulfillmentStatus:
                case FulfillmentStatus.NOT_STARTED | FulfillmentStatus.PREPARING:
                    pass
                case FulfillmentStatus.SHIPPED:
                    return CancellationNotAllowed(order=order)
                case unreachable_fulfillment:
                    assert_never(unreachable_fulfillment)
        case (
            OrderStatus.PENDING_PAYMENT
            | OrderStatus.PAYMENT_FAILED
            | OrderStatus.CANCEL_PENDING
            | OrderStatus.CANCELED
            | OrderStatus.EXPIRED
        ):
            return CancellationNotAllowed(order=order)
        case unreachable_order_status:
            assert_never(unreachable_order_status)

    requested_at = datetime.now(UTC)
    canceled_order = order.model_copy(
        update={
            "status": OrderStatus.CANCEL_PENDING,
            "cancelPendingAt": requested_at,
        },
    )
    cancellation = Cancellation(
        id=f"cancellation-{uuid4().hex[:12]}",
        orderId=order.id,
        reason=command.reason,
        orderStatus=OrderStatus.CANCEL_PENDING,
        refundStatus=RefundStatus.REQUESTED,
        createdAt=requested_at,
    )
    orders[command.order_id] = canceled_order
    cancellations[command.order_id] = InMemoryCancellationEntry(
        cancellation=cancellation,
        user_id=command.user_id,
        idempotency_key=command.idempotency_key,
    )
    return CancellationRequested(cancellation=cancellation)


def apply_in_memory_refund_completed(
    state: InMemoryRefundState,
    event: RefundCompletedEvent,
) -> bool:
    order_id = OrderId(event.orderId)
    order = state.orders.get(order_id)
    entry = state.cancellations.get(order_id)
    if (
        order is None
        or entry is None
        or not refund_event_is_valid(event)
        or not _refund_matches(event, entry, order)
        or event.eventId in state.processed_event_ids
    ):
        return False
    state.processed_event_ids.add(event.eventId)
    match order.status:
        case OrderStatus.CANCEL_PENDING:
            state.orders[order_id] = order.model_copy(
                update={
                    "status": OrderStatus.CANCELED,
                    "canceledAt": event.occurredAt,
                },
            )
            state.cancellations[order_id] = InMemoryCancellationEntry(
                cancellation=entry.cancellation.model_copy(
                    update={
                        "orderStatus": OrderStatus.CANCELED,
                        "refundStatus": RefundStatus.COMPLETED,
                        "completedAt": event.occurredAt,
                    },
                ),
                user_id=entry.user_id,
                idempotency_key=entry.idempotency_key,
            )
            return True
        case (
            OrderStatus.PENDING_PAYMENT
            | OrderStatus.CONFIRMED
            | OrderStatus.PAYMENT_FAILED
            | OrderStatus.CANCELED
            | OrderStatus.EXPIRED
        ):
            return False
        case unreachable_order_status:
            assert_never(unreachable_order_status)


def apply_in_memory_refund_failed(
    state: InMemoryRefundState,
    event: RefundFailedEvent,
) -> bool:
    order_id = OrderId(event.orderId)
    order = state.orders.get(order_id)
    entry = state.cancellations.get(order_id)
    if (
        order is None
        or entry is None
        or not refund_event_is_valid(event)
        or not _refund_matches(event, entry, order)
        or event.eventId in state.processed_event_ids
    ):
        return False
    state.processed_event_ids.add(event.eventId)
    match order.status:
        case OrderStatus.CANCEL_PENDING:
            match entry.cancellation.refundStatus:
                case RefundStatus.REQUESTED | RefundStatus.PROCESSING:
                    state.cancellations[order_id] = InMemoryCancellationEntry(
                        cancellation=entry.cancellation.model_copy(
                            update={"refundStatus": RefundStatus.FAILED},
                        ),
                        user_id=entry.user_id,
                        idempotency_key=entry.idempotency_key,
                    )
                    return True
                case RefundStatus.FAILED | RefundStatus.COMPLETED:
                    return False
                case unreachable_refund_status:
                    assert_never(unreachable_refund_status)
        case (
            OrderStatus.PENDING_PAYMENT
            | OrderStatus.CONFIRMED
            | OrderStatus.PAYMENT_FAILED
            | OrderStatus.CANCELED
            | OrderStatus.EXPIRED
        ):
            return False
        case unreachable_order_status:
            assert_never(unreachable_order_status)


def _refund_matches(
    event: RefundCompletedEvent | RefundFailedEvent,
    entry: InMemoryCancellationEntry,
    order: Order,
) -> bool:
    return (
        event.refundId == entry.cancellation.id
        and event.paymentId == order.paymentId
        and event.userId == order.userId
        and event.amount == order.amount
        and event.sourceId == entry.cancellation.id
    )
