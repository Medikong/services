from datetime import UTC, datetime

from app.counter_repository import MIN_VIEWS_FOR_RATIO, RECENCY_WINDOW
from app.models import DropId, DropInterestStats, UpcomingRankingItem
from app.view_store import DropViewCounterStore


class DropInterestCounterStore:
    """In-memory DropInterestCounterRepository 구현. DATABASE_URL이 없을 때(로컬/테스트) 쓰인다."""

    def __init__(self, view_counter_store: DropViewCounterStore) -> None:
        self._counts: dict[DropId, int] = {}
        self._updated_at: dict[DropId, datetime] = {}
        self._view_counter_store = view_counter_store

    async def increment(self, drop_id: DropId) -> None:
        self._counts[drop_id] = self._counts.get(drop_id, 0) + 1
        self._updated_at[drop_id] = datetime.now(UTC)

    async def decrement(self, drop_id: DropId) -> None:
        self._counts[drop_id] = max(self._counts.get(drop_id, 0) - 1, 0)
        self._updated_at[drop_id] = datetime.now(UTC)

    async def get(self, drop_id: DropId) -> DropInterestStats | None:
        count = self._counts.get(drop_id)
        if count is None:
            return None
        return DropInterestStats(dropId=drop_id, interestCount=count)

    async def list_by_interest_count(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[UpcomingRankingItem], bool]:
        now = datetime.now(UTC)
        recency_cutoff = now - RECENCY_WINDOW

        candidates: list[tuple[DropId, int, int, bool, float | None]] = []
        for drop_id, interest_count in self._counts.items():
            last_interest_at = self._updated_at.get(drop_id)
            last_viewed_at = self._view_counter_store.last_viewed_at.get(drop_id)
            activity_times = [t for t in (last_interest_at, last_viewed_at) if t is not None]
            if not activity_times or max(activity_times) < recency_cutoff:
                continue  # 최근 활동 게이트: 둘 다 오래됐으면 후보 제외("얼어붙은 비율" 방지)

            view_count = self._view_counter_store.view_counts.get(drop_id, 0)
            meets_threshold = view_count >= MIN_VIEWS_FOR_RATIO
            conversion_rate = (interest_count / view_count) if meets_threshold else None
            candidates.append((drop_id, interest_count, view_count, meets_threshold, conversion_rate))

        # cursor는 offset 문자열이다(Postgres 구현과 동일한 규약).
        ranked = sorted(
            candidates,
            key=lambda c: (
                0 if c[3] else 1,  # 1티어: 표본 충분(전환율 정렬) 먼저, 2티어: 표본 부족(찜수 폴백) 뒤
                -(c[4] or 0.0),
                -c[1],
                c[0],
            ),
        )
        offset = int(cursor) if cursor is not None else 0
        selected = ranked[offset : offset + limit]
        has_next = offset + limit < len(ranked)
        items = [
            UpcomingRankingItem(
                dropId=drop_id,
                interestCount=interest_count,
                viewCount=view_count,
                conversionRate=conversion_rate,
            )
            for drop_id, interest_count, view_count, _, conversion_rate in selected
        ]
        return items, has_next
