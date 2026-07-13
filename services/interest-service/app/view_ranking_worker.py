import asyncio
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime, timedelta
from zoneinfo import ZoneInfo

from app.view_repository import DropViewRankingRepository

KST = ZoneInfo("Asia/Seoul")
BUCKET_WIDTH = timedelta(hours=3)
GRACE_PERIOD = timedelta(minutes=1)
RANKING_LIMIT = 100


def bucket_boundaries_for(now: datetime) -> tuple[datetime, datetime]:
    """`now` 시점 기준 가장 최근에 닫힌 KST 3시간 구간의 [시작, 끝)을 반환한다."""
    now_kst = now.astimezone(KST)
    bucket_end = now_kst.replace(hour=(now_kst.hour // 3) * 3, minute=0, second=0, microsecond=0)
    return bucket_end - BUCKET_WIDTH, bucket_end


def next_run_at(now: datetime) -> datetime:
    """다음 KST 3시간 경계 + 유예 시간을 반환한다(배치가 구간 마감 직후에 돌도록)."""
    _, bucket_end = bucket_boundaries_for(now)
    next_boundary = bucket_end + BUCKET_WIDTH
    return next_boundary + GRACE_PERIOD


async def run_view_ranking_worker_forever(
    repository: DropViewRankingRepository,
    *,
    now_fn: Callable[[], datetime] = lambda: datetime.now(UTC),
    sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
) -> None:
    """실시간 조회 랭킹 배치 Worker(`SD.A.0730`).

    시작하자마자 가장 최근에 닫힌 구간을 한 번 계산한 뒤(서비스 재시작 직후의 결측 방지),
    다음 KST 3시간 경계까지 자고 다시 계산하기를 반복한다. `compute_and_store_bucket`은
    같은 구간을 다시 계산해도 안전하다(기존 스냅샷을 지우고 새로 씀).
    """
    while True:
        now = now_fn()
        bucket_start, bucket_end = bucket_boundaries_for(now)
        await repository.compute_and_store_bucket(bucket_start, bucket_end, RANKING_LIMIT)
        wait_seconds = (next_run_at(now) - now_fn()).total_seconds()
        await sleep(max(wait_seconds, 0))
