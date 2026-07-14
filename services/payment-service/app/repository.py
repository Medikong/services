from typing import Protocol

from contracts import OrderCreatedEvent

from app.models import Payment, PaymentId
from app.store import (
    ApprovePaymentCommand,
    ApprovePaymentResult,
    FailPaymentCommand,
    FailPaymentResult,
    KnownOrder,
)


class PaymentRepository(Protocol):
    async def approve_mock_payment(
        self, command: ApprovePaymentCommand
    ) -> ApprovePaymentResult: ...

    async def fail_mock_payment(
        self, command: FailPaymentCommand
    ) -> FailPaymentResult: ...

    async def get_payment(self, payment_id: PaymentId) -> Payment | None: ...

    async def record_order_created(self, event: OrderCreatedEvent) -> KnownOrder: ...

    async def get_known_order(self, order_id: str) -> KnownOrder | None: ...
