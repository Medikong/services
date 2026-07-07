from typing import Protocol

from contracts import PaymentApprovedEvent, PaymentFailedEvent

from app.models import Order, OrderId
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    PaymentApprovalResult,
    PaymentFailureResult,
)


class OrderRepository(Protocol):
    async def create_order(self, command: CreateOrderCommand) -> CreateOrderResult: ...

    async def get_order(self, order_id: OrderId) -> Order | None: ...

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult: ...

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult: ...
