from contracts import RefundCompletedEvent, RefundFailedEvent


def refund_event_is_valid(
    event: RefundCompletedEvent | RefundFailedEvent,
) -> bool:
    text_limits = (
        (event.eventId, 128),
        (event.userId, 64),
        (event.sourceId, 64),
        (event.producer, 64),
        (event.refundId, 64),
        (event.orderId, 64),
        (event.paymentId, 64),
    )
    return (
        event.producer == "payment-service"
        and event.occurredAt.utcoffset() is not None
        and (not isinstance(event, RefundFailedEvent) or "\x00" not in event.reason)
        and all(
            0 < len(value) <= limit and "\x00" not in value
            for value, limit in text_limits
        )
    )
