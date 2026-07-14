from typing import Protocol

from app.models import DropId, DropInterestStats, UpcomingRankingItem


class DropInterestCounterRepository(Protocol):
    """DropInterestCounter(AGG.A.07-02) 저장소.

    누적 활성 찜 수를 유지한다 — 당일 리셋 없음. 근거: 찜은 반복 발생하는
    행동이 아니라 한 번 누르고 유지되는 상태라, "오늘 순증감"보다
    "지금 몇 명이 찜하고 있나"(누적치)가 오픈 전 드롭의 관심도를 더
    안정적으로 나타낸다(2026-07-14 랭킹 설계 확정, 티켓팅 사이트
    실시간 예매 랭킹이 판매 신호와 관심 신호를 섞지 않는 사례로 검증).
    """

    async def increment(self, drop_id: DropId) -> None: ...

    async def decrement(self, drop_id: DropId) -> None: ...

    async def get(self, drop_id: DropId) -> DropInterestStats | None: ...

    async def list_by_interest_count(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[UpcomingRankingItem], bool]: ...
