from datetime import UTC, datetime
from typing import Final, assert_never

import anyio

from app.refunds import (
    RefundExecutionRepository,
    RefundProvider,
    RefundProviderCompleted,
    RefundProviderFailed,
)

REFUND_IDLE_DELAY_SECONDS: Final = 0.25


class RefundWorker:
    def __init__(
        self,
        repository: RefundExecutionRepository,
        provider: RefundProvider,
    ) -> None:
        self._repository: RefundExecutionRepository = repository
        self._provider: RefundProvider = provider

    async def process_once(self, now: datetime | None = None) -> bool:
        attempted_at = now or datetime.now(UTC)
        attempt = await self._repository.claim_due_refund(attempted_at)
        if attempt is None:
            return False
        result = await self._provider.refund(attempt)
        match result:
            case RefundProviderCompleted():
                return await self._repository.complete_refund(attempt, attempted_at)
            case RefundProviderFailed(reason=reason):
                return await self._repository.fail_refund(
                    attempt,
                    reason,
                    attempted_at,
                )
            case unreachable:
                assert_never(unreachable)

    async def run(self) -> None:
        while True:
            attempted = await self.process_once()
            if not attempted:
                await anyio.sleep(REFUND_IDLE_DELAY_SECONDS)
