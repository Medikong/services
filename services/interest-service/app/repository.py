from typing import Protocol

from app.models import Interest, UserId
from app.store import ToggleInterestCommand, ToggleInterestResult


class InterestRepository(Protocol):
    async def upsert_status(self, command: ToggleInterestCommand) -> ToggleInterestResult: ...

    async def list_active_by_user(
        self,
        user_id: UserId,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[Interest], bool]: ...
