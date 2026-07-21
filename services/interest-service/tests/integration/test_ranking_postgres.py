"""실제 Postgres에 붙는 통합 테스트.

이 서비스는 Alembic 없이 `Base.metadata.create_all()`로 스키마를 만든다(app/db.py의
`create_schema`) — 그래서 catalog/order-service처럼 "마이그레이션 적용" 자체를 검증할
필요는 없지만, 반대로 스키마 드리프트를 잡아줄 마이그레이션 도구가 아예 없다는 뜻이라
실제 DB의 컬럼 타입/제약조건에 의존하는 로직(JOIN, CASE, UNIQUE 제약 등)은 인메모리
단위 테스트가 절대 못 잡는다. 여기서 다루는 4가지는 전부 2026-07-21 세션에서 curl로
수동 검증했던 시나리오를 영구 회귀 테스트로 옮긴 것이다.
"""

import asyncio
import os
from datetime import UTC, datetime, timedelta

import anyio
import pytest
from sqlalchemy import text
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app.counter_repository import MIN_VIEWS_FOR_RATIO, RECENCY_WINDOW
from app.db import create_schema
from app.models import DropId, InterestStatus, UserId
from app.postgres import (
    Base,
    PostgresDropInterestCounterRepository,
    PostgresDropViewCounterRepository,
    PostgresInterestRepository,
)
from app.store import InterestChanged, InterestUnchanged, ToggleInterestCommand

DATABASE_URL = os.getenv("TEST_DATABASE_URL")
pytestmark = pytest.mark.skipif(
    DATABASE_URL is None,
    reason="TEST_DATABASE_URL is required for interest-service PostgreSQL tests",
)


def _engine_and_sessions() -> tuple:
    assert DATABASE_URL is not None
    engine = create_async_engine(DATABASE_URL)
    return engine, async_sessionmaker(engine, expire_on_commit=False)


async def _reset_schema(engine) -> None:
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.drop_all)
    await create_schema(engine)


def test_conversion_rate_ties_break_by_raw_interest_count() -> None:
    """동일 전환율일 때 원시 찜수가 많은 쪽이 앞선다 (07-21 curl 검증 #1)."""

    async def run() -> None:
        engine, sessions = _engine_and_sessions()
        await _reset_schema(engine)
        try:
            counters = PostgresDropInterestCounterRepository(sessions)
            views = PostgresDropViewCounterRepository(sessions)

            for _ in range(40):
                await counters.increment(DropId("drop-shoe"))
            for _ in range(100):
                await views.increment(DropId("drop-shoe"))

            for _ in range(8):
                await counters.increment(DropId("drop-old"))
            for _ in range(20):
                await views.increment(DropId("drop-old"))

            items, has_next = await counters.list_by_interest_count(limit=10, cursor=None)

            assert [item.dropId for item in items] == ["drop-shoe", "drop-old"]
            assert items[0].conversionRate == pytest.approx(0.4)
            assert items[1].conversionRate == pytest.approx(0.4)
            assert has_next is False
        finally:
            await engine.dispose()

    anyio.run(run)


def test_recency_gate_excludes_stale_high_ratio_drop() -> None:
    """활동이 RECENCY_WINDOW보다 오래 멈춘 드롭은, DB에 높은 비율이 남아있어도
    랭킹에서 완전히 빠진다 — "얼어붙은 비율" 결함이 실제로 해결됐는지 확인 (07-21 curl 검증 #2)."""

    async def run() -> None:
        engine, sessions = _engine_and_sessions()
        await _reset_schema(engine)
        try:
            counters = PostgresDropInterestCounterRepository(sessions)
            views = PostgresDropViewCounterRepository(sessions)

            for _ in range(8):
                await counters.increment(DropId("drop-old"))
            for _ in range(20):
                await views.increment(DropId("drop-old"))

            items_before, _ = await counters.list_by_interest_count(limit=10, cursor=None)
            assert [item.dropId for item in items_before] == ["drop-old"]

            stale_at = datetime.now(UTC) - RECENCY_WINDOW - timedelta(days=1)
            async with sessions() as session:
                await session.execute(
                    text("UPDATE drop_interest_counters SET updated_at = :ts WHERE drop_id = 'drop-old'"),
                    {"ts": stale_at},
                )
                await session.execute(
                    text("UPDATE drop_view_counters SET last_viewed_at = :ts WHERE drop_id = 'drop-old'"),
                    {"ts": stale_at},
                )
                await session.commit()

            items_after, _ = await counters.list_by_interest_count(limit=10, cursor=None)
            assert items_after == []
        finally:
            await engine.dispose()

    anyio.run(run)


def test_low_sample_drop_falls_back_to_raw_interest_count_tier() -> None:
    """조회수가 MIN_VIEWS_FOR_RATIO 미만이면 conversionRate=null로, 비율 티어보다
    항상 아래인 원시 찜수 폴백 티어에 배치된다 (07-21 curl 검증 #3)."""

    async def run() -> None:
        engine, sessions = _engine_and_sessions()
        await _reset_schema(engine)
        try:
            counters = PostgresDropInterestCounterRepository(sessions)
            views = PostgresDropViewCounterRepository(sessions)

            for _ in range(40):
                await counters.increment(DropId("drop-shoe"))
            for _ in range(100):
                await views.increment(DropId("drop-shoe"))

            assert MIN_VIEWS_FOR_RATIO > 3, "표본 임계값 상수가 바뀌면 아래 3이 더는 '미달'이 아닐 수 있다"
            for _ in range(100):
                await counters.increment(DropId("drop-bag"))
            for _ in range(3):
                await views.increment(DropId("drop-bag"))

            items, _ = await counters.list_by_interest_count(limit=10, cursor=None)

            assert [item.dropId for item in items] == ["drop-shoe", "drop-bag"]
            assert items[0].conversionRate == pytest.approx(0.4)
            assert items[1].conversionRate is None
            assert items[1].interestCount == 100
        finally:
            await engine.dispose()

    anyio.run(run)


def test_concurrent_first_interest_does_not_crash_on_unique_constraint() -> None:
    """한 번도 찜한 적 없는 (user, drop)에 동시에 여러 '찜 추가'가 들어와도
    UniqueConstraint 위반(IntegrityError)이 API까지 새어나가지 않고 1개만 남는다.

    이 동시성 버그는 훨씬 이전 세션에서 발견/수정됐지만 그때는 1회성 스크립트로만
    검증하고 영구 회귀 테스트로 남기지 않았었다 — 여기서 처음으로 자동화된다.
    """

    async def run() -> None:
        engine, sessions = _engine_and_sessions()
        await _reset_schema(engine)
        try:
            repository = PostgresInterestRepository(sessions)
            command = ToggleInterestCommand(
                user_id=UserId("user-race"),
                drop_id=DropId("drop-race"),
                target_status=InterestStatus.ACTIVE,
            )

            results = await asyncio.gather(*[repository.upsert_status(command) for _ in range(5)])

            changed = [r for r in results if isinstance(r, InterestChanged)]
            unchanged = [r for r in results if isinstance(r, InterestUnchanged)]
            assert len(changed) == 1
            assert len(unchanged) == 4

            async with sessions() as session:
                count = await session.scalar(
                    text(
                        "SELECT COUNT(*) FROM interests WHERE user_id = 'user-race' AND drop_id = 'drop-race'",
                    ),
                )
            assert count == 1
        finally:
            await engine.dispose()

    anyio.run(run)
