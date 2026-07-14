from contracts import FulfillmentStatus

from app.models import Order, OrderStatus
from app.records import OrderRecord


def order_from_record(record: OrderRecord) -> Order:
    """Map a persisted order row to the domain response model."""
    return Order(
        id=record.id,
        userId=record.user_id,
        dropId=record.drop_id,
        productId=record.product_id,
        quantity=record.quantity,
        amount=record.amount,
        status=OrderStatus(record.status),
        fulfillmentStatus=FulfillmentStatus(record.fulfillment_status),
        paymentId=record.payment_id,
        createdAt=record.created_at,
        confirmedAt=record.confirmed_at,
        cancelPendingAt=record.cancel_pending_at,
        canceledAt=record.canceled_at,
    )
