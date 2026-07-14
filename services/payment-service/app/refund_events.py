from datetime import datetime

from contracts import RefundCompletedEvent, RefundFailedEvent

from app.refunds import RefundAttempt

PRODUCER_NAME = "payment-service"


def refund_completed_event(
    attempt: RefundAttempt,
    occurred_at: datetime,
) -> RefundCompletedEvent:
    return RefundCompletedEvent(
        eventId=f"evt-refund-completed-{attempt.refund_id}",
        userId=attempt.user_id,
        sourceId=attempt.refund_id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        correlationId=attempt.order_id,
        refundId=attempt.refund_id,
        orderId=attempt.order_id,
        paymentId=attempt.payment_id,
        amount=attempt.amount,
    )


def refund_failed_event(
    attempt: RefundAttempt,
    reason: str,
    occurred_at: datetime,
) -> RefundFailedEvent:
    return RefundFailedEvent(
        eventId=f"evt-refund-failed-{attempt.refund_id}",
        userId=attempt.user_id,
        sourceId=attempt.refund_id,
        occurredAt=occurred_at,
        producer=PRODUCER_NAME,
        correlationId=attempt.order_id,
        refundId=attempt.refund_id,
        orderId=attempt.order_id,
        paymentId=attempt.payment_id,
        amount=attempt.amount,
        reason=reason,
    )
