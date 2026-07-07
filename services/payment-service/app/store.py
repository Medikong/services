from dataclasses import dataclass
from datetime import UTC, datetime
from typing import assert_never

from contracts import OrderCreatedEvent

from app.models import (
    IdempotencyKey,
    OrderId,
    Payment,
    PaymentId,
    PaymentMethod,
    PaymentStatus,
    UserId,
)


@dataclass(frozen=True, slots=True)
class ApprovePaymentCommand:
    user_id: UserId
    order_id: OrderId
    amount: int
    method: PaymentMethod
    idempotency_key: IdempotencyKey


@dataclass(frozen=True, slots=True)
class FailPaymentCommand:
    user_id: UserId
    order_id: OrderId
    amount: int
    method: PaymentMethod
    idempotency_key: IdempotencyKey
    reason: str | None


@dataclass(frozen=True, slots=True)
class PaymentApproved:
    payment: Payment


@dataclass(frozen=True, slots=True)
class PaymentAlreadyApproved:
    payment: Payment


@dataclass(frozen=True, slots=True)
class PaymentFailed:
    payment: Payment


@dataclass(frozen=True, slots=True)
class PaymentAlreadyFailed:
    payment: Payment


@dataclass(frozen=True, slots=True)
class PaymentOrderNotFound:
    order_id: OrderId


@dataclass(frozen=True, slots=True)
class PaymentOrderMismatch:
    order_id: OrderId


@dataclass(frozen=True, slots=True)
class PaymentIdempotencyConflict:
    payment: Payment


type ApprovePaymentResult = (
    PaymentApproved
    | PaymentAlreadyApproved
    | PaymentOrderNotFound
    | PaymentOrderMismatch
    | PaymentIdempotencyConflict
)


type FailPaymentResult = (
    PaymentFailed
    | PaymentAlreadyFailed
    | PaymentOrderNotFound
    | PaymentOrderMismatch
    | PaymentIdempotencyConflict
)


@dataclass(frozen=True, slots=True)
class KnownOrder:
    order_id: OrderId
    user_id: UserId
    amount: int


class PaymentStore:
    def __init__(self) -> None:
        self._payments: dict[PaymentId, Payment] = {}
        self._idempotency_index: dict[tuple[UserId, IdempotencyKey], PaymentId] = {}
        self._known_orders: dict[OrderId, KnownOrder] = {}
        self._next_payment_number = 1

    async def approve_mock_payment(self, command: ApprovePaymentCommand) -> ApprovePaymentResult:
        replayed_payment = self._replayed_payment(command.user_id, command.idempotency_key)
        if replayed_payment is not None:
            if not payment_matches_command(replayed_payment, command):
                return PaymentIdempotencyConflict(payment=replayed_payment)
            return PaymentAlreadyApproved(payment=replayed_payment)

        known_order = self._known_orders.get(command.order_id)
        if known_order is None:
            return PaymentOrderNotFound(order_id=command.order_id)
        if not known_order_matches_command(known_order, command):
            return PaymentOrderMismatch(order_id=command.order_id)

        approved_at = datetime.now(UTC)
        payment_id = self._next_payment_id()
        payment = Payment(
            id=payment_id,
            orderId=command.order_id,
            userId=command.user_id,
            amount=command.amount,
            method=command.method,
            status=PaymentStatus.APPROVED,
            createdAt=approved_at,
            approvedAt=approved_at,
        )
        self._payments[payment_id] = payment
        self._idempotency_index[(command.user_id, command.idempotency_key)] = payment_id
        return PaymentApproved(payment=payment)

    async def fail_mock_payment(self, command: FailPaymentCommand) -> FailPaymentResult:
        replayed_payment = self._replayed_payment(command.user_id, command.idempotency_key)
        if replayed_payment is not None:
            if not failed_payment_matches_command(replayed_payment, command):
                return PaymentIdempotencyConflict(payment=replayed_payment)
            return PaymentAlreadyFailed(payment=replayed_payment)

        known_order = self._known_orders.get(command.order_id)
        if known_order is None:
            return PaymentOrderNotFound(order_id=command.order_id)
        if not known_order_matches_command(known_order, command):
            return PaymentOrderMismatch(order_id=command.order_id)

        failed_at = datetime.now(UTC)
        payment_id = self._next_payment_id()
        payment = Payment(
            id=payment_id,
            orderId=command.order_id,
            userId=command.user_id,
            amount=command.amount,
            method=command.method,
            status=PaymentStatus.FAILED,
            createdAt=failed_at,
            failedAt=failed_at,
            failureReason=command.reason,
        )
        self._payments[payment_id] = payment
        self._idempotency_index[(command.user_id, command.idempotency_key)] = payment_id
        return PaymentFailed(payment=payment)

    async def get_payment(self, payment_id: PaymentId) -> Payment | None:
        return self._payments.get(payment_id)

    async def record_order_created(self, event: OrderCreatedEvent) -> KnownOrder:
        order_id = OrderId(event.orderId)
        known_order = KnownOrder(
            order_id=OrderId(event.orderId),
            user_id=UserId(event.userId),
            amount=event.amount,
        )
        self._known_orders[order_id] = known_order
        return known_order

    async def get_known_order(self, order_id: str) -> KnownOrder | None:
        return self._known_orders.get(OrderId(order_id))

    def _replayed_payment(
        self,
        user_id: UserId,
        idempotency_key: IdempotencyKey,
    ) -> Payment | None:
        payment_id = self._idempotency_index.get((user_id, idempotency_key))
        if payment_id is None:
            return None
        return self._payments[payment_id]

    def _next_payment_id(self) -> PaymentId:
        payment_id = PaymentId(f"payment-{self._next_payment_number:03d}")
        self._next_payment_number += 1
        return payment_id


def approval_should_publish(result: ApprovePaymentResult) -> Payment | None:
    match result:
        case PaymentApproved(payment=payment):
            return payment
        case PaymentAlreadyApproved():
            return None
        case PaymentOrderNotFound() | PaymentOrderMismatch() | PaymentIdempotencyConflict():
            return None
        case unreachable:
            assert_never(unreachable)


def failure_should_publish(result: FailPaymentResult) -> Payment | None:
    match result:
        case PaymentFailed(payment=payment):
            return payment
        case PaymentAlreadyFailed():
            return None
        case PaymentOrderNotFound() | PaymentOrderMismatch() | PaymentIdempotencyConflict():
            return None
        case unreachable:
            assert_never(unreachable)


def payment_matches_command(payment: Payment, command: ApprovePaymentCommand) -> bool:
    return (
        payment.status == PaymentStatus.APPROVED
        and payment.orderId == command.order_id
        and payment.userId == command.user_id
        and payment.amount == command.amount
        and payment.method == command.method
    )


def failed_payment_matches_command(payment: Payment, command: FailPaymentCommand) -> bool:
    return (
        payment.status == PaymentStatus.FAILED
        and payment.orderId == command.order_id
        and payment.userId == command.user_id
        and payment.amount == command.amount
        and payment.method == command.method
        and payment.failureReason == command.reason
    )


def known_order_matches_command(
    known_order: KnownOrder,
    command: ApprovePaymentCommand | FailPaymentCommand,
) -> bool:
    return (
        known_order.user_id == command.user_id
        and known_order.amount == command.amount
    )
