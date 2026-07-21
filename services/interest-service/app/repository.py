from datetime import datetime
from typing import Protocol

from app.models import DropId, Interest, UserId
from app.store import ToggleInterestCommand, ToggleInterestResult


class InterestRepository(Protocol):
    async def upsert_status(self, command: ToggleInterestCommand) -> ToggleInterestResult: ...

    async def list_active_by_user(
        self,
        user_id: UserId,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[Interest], bool]: ...

    async def count_active_updated_in_window(
        self,
        start: datetime,
        end: datetime,
    ) -> dict[DropId, int]:
        """[start, end) 구간에 활성 찜 상태로 갱신된(신규 포함) 건수를 드롭별로 센다.

        실시간 조회 랭킹 배치의 '찜 속도' 신호(2026-07-20, 김정엽 멘토링 피드백 대응)에 쓰인다.
        """
        ...
