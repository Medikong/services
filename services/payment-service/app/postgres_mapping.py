from datetime import UTC, datetime
from uuid import uuid4

from app.models import OrderId, Payment, PaymentId, PaymentMethod, PaymentStatus, UserId
from app.records import KnownOrderRecord, PaymentRecord
from app.store import ApprovePaymentCommand, FailPaymentCommand, KnownOrder


def new_approved_payment(command: ApprovePaymentCommand) -> Payment:
    approved_at = datetime.now(UTC)
    return Payment(
        id=new_payment_id(),
        orderId=command.order_id,
        userId=command.user_id,
        amount=command.amount,
        method=command.method,
        status=PaymentStatus.APPROVED,
        createdAt=approved_at,
        approvedAt=approved_at,
    )


def new_failed_payment(command: FailPaymentCommand) -> Payment:
    failed_at = datetime.now(UTC)
    return Payment(
        id=new_payment_id(),
        orderId=command.order_id,
        userId=command.user_id,
        amount=command.amount,
        method=command.method,
        status=PaymentStatus.FAILED,
        createdAt=failed_at,
        failedAt=failed_at,
        failureReason=command.reason,
    )


def new_payment_id() -> PaymentId:
    return PaymentId(f"payment-{uuid4().hex[:12]}")


def record_from_payment(payment: Payment, idempotency_key: str) -> PaymentRecord:
    return PaymentRecord(
        id=payment.id,
        order_id=payment.orderId,
        user_id=payment.userId,
        amount=payment.amount,
        method=payment.method.value,
        status=payment.status.value,
        idempotency_key=idempotency_key,
        created_at=payment.createdAt,
        approved_at=payment.approvedAt,
        failed_at=payment.failedAt,
        failure_reason=payment.failureReason,
    )


def payment_from_record(record: PaymentRecord) -> Payment:
    return Payment(
        id=record.id,
        orderId=record.order_id,
        userId=record.user_id,
        amount=record.amount,
        method=PaymentMethod(record.method),
        status=PaymentStatus(record.status),
        createdAt=record.created_at,
        approvedAt=record.approved_at,
        failedAt=record.failed_at,
        failureReason=record.failure_reason,
    )


def known_order_from_record(record: KnownOrderRecord) -> KnownOrder:
    return KnownOrder(
        order_id=OrderId(record.order_id),
        user_id=UserId(record.user_id),
        amount=record.amount,
    )
