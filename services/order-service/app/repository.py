from typing import Protocol

from contracts import (
    PaymentApprovedEvent,
    PaymentFailedEvent,
    RefundCompletedEvent,
    RefundFailedEvent,
)

from app.cancellations import RequestCancellationCommand, RequestCancellationResult
from app.models import Cancellation, Order, OrderId, UserId
from app.store import (
    CreateOrderCommand,
    CreateOrderResult,
    PaymentApprovalResult,
    PaymentFailureResult,
)


class OrderRepository(Protocol):
    async def create_order(self, command: CreateOrderCommand) -> CreateOrderResult: ...

    async def get_order(self, order_id: OrderId) -> Order | None: ...

    async def request_cancellation(
        self,
        command: RequestCancellationCommand,
    ) -> RequestCancellationResult: ...

    async def get_cancellation(
        self,
        order_id: OrderId,
        user_id: UserId,
    ) -> Cancellation | None: ...

    async def apply_payment_approved(
        self,
        event: PaymentApprovedEvent,
    ) -> PaymentApprovalResult: ...

    async def apply_payment_failed(
        self,
        event: PaymentFailedEvent,
    ) -> PaymentFailureResult: ...

    async def apply_refund_completed(self, event: RefundCompletedEvent) -> bool: ...

    async def apply_refund_failed(self, event: RefundFailedEvent) -> bool: ...
