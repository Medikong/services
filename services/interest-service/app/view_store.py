from collections import defaultdict
from datetime import UTC, datetime, timedelta

from app.models import DropId, TrendingRankingItem, UserId
from app.repository import InterestRepository

RETENTION_WINDOW = timedelta(hours=3)


class DropViewStore:
    """In-memory DropViewRepository 구현. DATABASE_URL이 없을 때(로컬/테스트) 쓰인다."""

    def __init__(self) -> None:
        self.views: list[tuple[DropId, UserId, datetime]] = []

    async def record_view(self, drop_id: DropId, user_id: UserId) -> None:
        self.views.append((drop_id, user_id, datetime.now(UTC)))


class DropViewCounterStore:
    """In-memory DropViewCounterRepository 구현. 리셋 없는 누적 조회수 + 마지막 조회 시각을 유지한다."""

    def __init__(self) -> None:
        self.view_counts: dict[DropId, int] = {}
        self.last_viewed_at: dict[DropId, datetime] = {}

    async def increment(self, drop_id: DropId) -> None:
        self.view_counts[drop_id] = self.view_counts.get(drop_id, 0) + 1
        self.last_viewed_at[drop_id] = datetime.now(UTC)


class DropViewRankingStore:
    """In-memory DropViewRankingRepository 구현. DropViewStore와 짝을 이뤄 배치 로직을 검증한다."""

    def __init__(self, view_store: DropViewStore, interest_repository: InterestRepository) -> None:
        self._view_store = view_store
        self._interest_repository = interest_repository
        self._buckets: dict[datetime, list[TrendingRankingItem]] = {}
        self._latest_bucket_start: datetime | None = None

    async def compute_and_store_bucket(
        self,
        bucket_start: datetime,
        bucket_end: datetime,
        limit: int,
    ) -> None:
        counts: dict[DropId, set[UserId]] = defaultdict(set)
        for drop_id, user_id, viewed_at in self._view_store.views:
            if bucket_start <= viewed_at < bucket_end:
                counts[drop_id].add(user_id)

        new_interest_counts = await self._interest_repository.count_active_updated_in_window(
            bucket_start,
            bucket_end,
        )

        ranked = sorted(counts.items(), key=lambda item: (-len(item[1]), item[0]))[:limit]
        self._buckets[bucket_start] = [
            TrendingRankingItem(
                dropId=drop_id,
                rank=index + 1,
                viewerCount=len(users),
                newInterestCount=new_interest_counts.get(drop_id, 0),
                conversionRate=(new_interest_counts.get(drop_id, 0) / len(users)) if len(users) > 0 else None,
            )
            for index, (drop_id, users) in enumerate(ranked)
        ]
        if self._latest_bucket_start is None or bucket_start > self._latest_bucket_start:
            self._latest_bucket_start = bucket_start

        retention_cutoff = bucket_start - RETENTION_WINDOW
        self._view_store.views = [v for v in self._view_store.views if v[2] >= retention_cutoff]

    async def get_latest_bucket(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[TrendingRankingItem], bool, datetime | None]:
        if self._latest_bucket_start is None:
            return [], False, None
        items = self._buckets[self._latest_bucket_start]
        start_rank = int(cursor) if cursor is not None else 0
        filtered = [item for item in items if item.rank > start_rank]
        selected = filtered[:limit]
        has_next = len(filtered) > limit
        return selected, has_next, self._latest_bucket_start
