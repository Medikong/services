from datetime import datetime
from typing import Protocol

from app.models import DropId, TrendingRankingItem, UserId


class DropViewRepository(Protocol):
    async def record_view(self, drop_id: DropId, user_id: UserId) -> None: ...


class DropViewCounterRepository(Protocol):
    """드롭별 누적 조회수(리셋 없음) 저장소.

    `기다리는 상품 랭킹`(2026-07-21 재설계)의 전환율(찜/조회) 정렬과, 죽은 드롭을
    걸러내는 최근 활동 게이트(`last_viewed_at`) 둘 다에 쓰인다.
    """

    async def increment(self, drop_id: DropId) -> None: ...


class DropViewRankingRepository(Protocol):
    """실시간 조회 랭킹 배치 Worker가 쓰는 저장소.

    `compute_and_store_bucket`은 KST 3시간 고정 구간을 집계해 스냅샷으로 저장하고,
    이미 스냅샷으로 만든 그 이전 구간의 `DropView` 원문은 함께 지운다(안전 마진 1구간,
    2026-07-14 결정 — 별도 청소 배치 없이 `drop_views`가 최대 6시간분만 유지되게 한다).
    """

    async def compute_and_store_bucket(
        self,
        bucket_start: datetime,
        bucket_end: datetime,
        limit: int,
    ) -> None: ...

    async def get_latest_bucket(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[TrendingRankingItem], bool, datetime | None]: ...
