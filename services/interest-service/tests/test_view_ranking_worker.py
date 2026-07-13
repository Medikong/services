from datetime import UTC, datetime, timedelta

import anyio
import pytest

from app.view_ranking_worker import RANKING_LIMIT, run_view_ranking_worker_forever


class StopLoop(Exception):
    pass


class FakeRankingRepository:
    def __init__(self) -> None:
        self.calls: list[tuple[datetime, datetime, int]] = []

    async def compute_and_store_bucket(self, bucket_start: datetime, bucket_end: datetime, limit: int) -> None:
        self.calls.append((bucket_start, bucket_end, limit))

    async def get_latest_bucket(self, limit: int, cursor: str | None):  # noqa: ANN201 - test double
        return [], False, None


def test_worker_computes_most_recently_closed_bucket_on_start() -> None:
    # Given: 2026-07-14 12:10 KST(=03:10 UTC) -> 가장 최근 마감 구간은 09:00~12:00 KST
    fixed_now = datetime(2026, 7, 14, 3, 10, tzinfo=UTC)

    async def fake_sleep(_seconds: float) -> None:
        raise StopLoop

    repo = FakeRankingRepository()

    # When
    async def run() -> None:
        await run_view_ranking_worker_forever(repo, now_fn=lambda: fixed_now, sleep=fake_sleep)

    with pytest.raises(StopLoop):
        anyio.run(run)

    # Then: 시작하자마자(sleep 전에) 직전 구간을 한 번 계산해야 한다 — 재시작 직후 결측 방지(worker docstring 참고).
    assert len(repo.calls) == 1
    bucket_start, bucket_end, limit = repo.calls[0]
    assert bucket_end - bucket_start == timedelta(hours=3)
    assert limit == RANKING_LIMIT


def test_worker_loops_and_recomputes_on_each_wake_up() -> None:
    # Given: sleep을 두 번 통과시키고 세 번째에 멈춘다 — 루프가 반복 실행되는지 확인.
    call_count = 0

    async def fake_sleep(_seconds: float) -> None:
        nonlocal call_count
        call_count += 1
        if call_count >= 2:
            raise StopLoop

    fixed_now = datetime(2026, 7, 14, 3, 10, tzinfo=UTC)
    repo = FakeRankingRepository()

    async def run() -> None:
        await run_view_ranking_worker_forever(repo, now_fn=lambda: fixed_now, sleep=fake_sleep)

    with pytest.raises(StopLoop):
        anyio.run(run)

    # Then: compute_and_store_bucket가 sleep 호출 횟수 + 1(시작 시 1회)만큼 실행돼야 한다.
    assert len(repo.calls) == 2
