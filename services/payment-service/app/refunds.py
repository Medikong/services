from dataclasses import dataclass
from datetime import datetime
from typing import Protocol

from contracts import RefundRequestedEvent, RefundStatus


@dataclass(frozen=True, slots=True)
class RefundAttempt:
    refund_id: str
    order_id: str
    payment_id: str
    user_id: str
    amount: int
    attempt: int


@dataclass(frozen=True, slots=True)
class RefundProviderCompleted:
    provider_reference: str


@dataclass(frozen=True, slots=True)
class RefundProviderFailed:
    reason: str


type RefundProviderResult = RefundProviderCompleted | RefundProviderFailed


class RefundProvider(Protocol):
    async def refund(self, attempt: RefundAttempt) -> RefundProviderResult: ...


class RefundRequestRepository(Protocol):
    async def record_refund_requested(self, event: RefundRequestedEvent) -> bool: ...


class RefundExecutionRepository(Protocol):
    async def claim_due_refund(self, now: datetime) -> RefundAttempt | None: ...

    async def complete_refund(self, attempt: RefundAttempt, now: datetime) -> bool: ...

    async def fail_refund(
        self,
        attempt: RefundAttempt,
        reason: str,
        now: datetime,
    ) -> bool: ...


class MockRefundProvider:
    def __init__(self, fail_attempts: frozenset[int] = frozenset()) -> None:
        self._fail_attempts: frozenset[int] = fail_attempts

    async def refund(self, attempt: RefundAttempt) -> RefundProviderResult:
        if attempt.attempt in self._fail_attempts:
            return RefundProviderFailed(
                reason=f"injected provider failure at attempt {attempt.attempt}",
            )
        return RefundProviderCompleted(
            provider_reference=f"mock-{attempt.refund_id}",
        )


REFUND_REQUESTED = RefundStatus.REQUESTED.value
REFUND_PROCESSING = RefundStatus.PROCESSING.value
REFUND_COMPLETED = RefundStatus.COMPLETED.value
REFUND_FAILED = RefundStatus.FAILED.value
