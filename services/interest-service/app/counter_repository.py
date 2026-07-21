from datetime import timedelta
from typing import Final, Protocol

from app.models import DropId, DropInterestStats, UpcomingRankingItem

# 2026-07-21 재설계(기간편향 대응, 김정엽 멘토링 피드백 후속 논의):
# 정렬 기준을 interestCount 단독에서 전환율(interestCount/viewCount)로 바꾸되,
# 두 가지 안전장치를 같이 둔다.
#
# RECENCY_WINDOW: 최근 이 기간 안에 조회든 찜이든 활동이 전혀 없는 드롭은 후보에서
# 제외한다("얼어붙은 비율" 문제 대응 — 인기 절정기에 찍어둔 높은 전환율이 활동이
# 끊긴 뒤에도 그대로 남아 영원히 상위권을 차지하는 것을 막는다). 도메인 문서
# (REQ_A_01/REQ_A_07)에 드롭의 전형적 지속 기간을 명시한 근거가 없어 잠정치로
# 14일을 둔다 — 실사용 데이터가 쌓이면 조정 대상.
RECENCY_WINDOW: Final = timedelta(days=14)

# MIN_VIEWS_FOR_RATIO: 조회수가 이 값 미만인 드롭은 전환율이 표본 부족으로
# 왜곡되기 쉬워(예: 조회 2/찜 1=50%) 전환율 정렬 대상에서 빼고, 대신 원시
# 누적 찜수로 순위를 매겨 하위 티어에 배치한다.
MIN_VIEWS_FOR_RATIO: Final = 20


class DropInterestCounterRepository(Protocol):
    """DropInterestCounter(AGG.A.07-02) 저장소.

    누적 활성 찜 수를 유지한다 — 리셋 없음. 근거: 찜은 반복 발생하는
    행동이 아니라 한 번 누르고 유지되는 상태라, "오늘 순증감"보다
    "지금 몇 명이 찜하고 있나"(누적치)가 관심도를 더 안정적으로
    나타낸다(2026-07-14 랭킹 설계 확정).

    `list_by_interest_count`의 정렬 기준은 2026-07-21에 전환율 기반으로
    바뀌었다 — 위 `RECENCY_WINDOW`/`MIN_VIEWS_FOR_RATIO` 참고.
    """

    async def increment(self, drop_id: DropId) -> None: ...

    async def decrement(self, drop_id: DropId) -> None: ...

    async def get(self, drop_id: DropId) -> DropInterestStats | None: ...

    async def list_by_interest_count(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[UpcomingRankingItem], bool]: ...
