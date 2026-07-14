from app.models import DropId, DropInterestStats, UpcomingRankingItem


class DropInterestCounterStore:
    """In-memory DropInterestCounterRepository 구현. DATABASE_URL이 없을 때(로컬/테스트) 쓰인다."""

    def __init__(self) -> None:
        self._counts: dict[DropId, int] = {}

    async def increment(self, drop_id: DropId) -> None:
        self._counts[drop_id] = self._counts.get(drop_id, 0) + 1

    async def decrement(self, drop_id: DropId) -> None:
        self._counts[drop_id] = max(self._counts.get(drop_id, 0) - 1, 0)

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
        # cursor는 offset 문자열이다(Postgres 구현과 동일한 규약 — main.py가 두 구현을
        # 같은 방식으로 호출할 수 있어야 한다).
        ranked = sorted(self._counts.items(), key=lambda item: (-item[1], item[0]))
        offset = int(cursor) if cursor is not None else 0
        selected = ranked[offset : offset + limit]
        has_next = offset + limit < len(ranked)
        items = [UpcomingRankingItem(dropId=drop_id, interestCount=count) for drop_id, count in selected]
        return items, has_next
