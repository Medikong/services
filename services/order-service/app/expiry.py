from collections.abc import Callable
from datetime import UTC, datetime
import logging
from typing import Final, Protocol

import anyio
from sqlalchemy.exc import SQLAlchemyError

EXPIRY_IDLE_DELAY_SECONDS: Final = 0.25
LOGGER: Final = logging.getLogger(__name__)
type Clock = Callable[[], datetime]


class DueOrderExpirer(Protocol):
    async def expire_due_order(self, now: datetime) -> bool: ...


def utc_now() -> datetime:
    return datetime.now(UTC)


class OrderExpiryWorker:
    def __init__(self, repository: DueOrderExpirer, clock: Clock = utc_now) -> None:
        self._repository: DueOrderExpirer = repository
        self._clock: Clock = clock

    async def process_once(self) -> bool:
        return await self._repository.expire_due_order(self._clock())

    async def run(self) -> None:
        while True:
            try:
                processed = await self.process_once()
            except (SQLAlchemyError, OSError, RuntimeError) as error:
                LOGGER.warning(
                    "order expiry worker restarting after %s",
                    type(error).__name__,
                )
                processed = False
            if not processed:
                await anyio.sleep(EXPIRY_IDLE_DELAY_SECONDS)
