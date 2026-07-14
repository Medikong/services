from contracts import PaymentApprovedEvent, PaymentFailedEvent

from app.models import Payment

PRODUCER_NAME = "payment-service"


def payment_approved_event(payment: Payment) -> PaymentApprovedEvent:
    return PaymentApprovedEvent(
        eventId=f"evt-payment-approved-{payment.id}",
        userId=payment.userId,
        sourceId=payment.id,
        occurredAt=payment.approvedAt or payment.createdAt,
        producer=PRODUCER_NAME,
        orderId=payment.orderId,
        paymentId=payment.id,
        amount=payment.amount,
        correlationId=payment.orderId,
    )


def payment_failed_event(payment: Payment) -> PaymentFailedEvent:
    return PaymentFailedEvent(
        eventId=f"evt-payment-failed-{payment.id}",
        userId=payment.userId,
        sourceId=payment.id,
        occurredAt=payment.failedAt or payment.createdAt,
        producer=PRODUCER_NAME,
        orderId=payment.orderId,
        paymentId=payment.id,
        amount=payment.amount,
        reason=payment.failureReason,
        correlationId=payment.orderId,
    )
