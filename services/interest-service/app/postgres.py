from datetime import UTC, datetime, timedelta
from uuid import uuid4

from sqlalchemy import BigInteger, DateTime, Integer, String, UniqueConstraint, func, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

from app.models import (
    DropId,
    DropInterestStats,
    Interest,
    InterestStatus,
    TrendingRankingItem,
    UpcomingRankingItem,
    UserId,
)
from app.store import (
    InterestChanged,
    InterestToggleConflict,
    InterestUnchanged,
    ToggleInterestCommand,
    ToggleInterestResult,
)

VIEW_RANKING_RETENTION_WINDOW = timedelta(hours=3)

# service-design.md CMD.A.07-01: "낙관적 잠금(version) 충돌 시 재조회 후 1회 재시도, 재충돌 시 409로 응답"
MAX_OPTIMISTIC_LOCK_RETRIES = 1


class Base(DeclarativeBase):
    pass


class InterestRecord(Base):
    __tablename__ = "interests"
    __table_args__ = (UniqueConstraint("user_id", "drop_id", name="uq_interests_user_drop"),)

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    drop_id: Mapped[str] = mapped_column(String(64), nullable=False)
    status: Mapped[str] = mapped_column(String(16), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    version: Mapped[int] = mapped_column(Integer, nullable=False, default=0)


class PostgresInterestRepository:
    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def upsert_status(self, command: ToggleInterestCommand) -> ToggleInterestResult:
        for _ in range(MAX_OPTIMISTIC_LOCK_RETRIES + 1):
            result = await self._try_upsert_status(command)
            if result is not None:
                return result
        return InterestToggleConflict()

    async def _try_upsert_status(self, command: ToggleInterestCommand) -> ToggleInterestResult | None:
        """None을 반환하면 낙관적 잠금 충돌이니 상위에서 재시도한다."""
        async with self._session_factory() as session:
            record = await self._find(session, command.user_id, command.drop_id)
            now = datetime.now(UTC)

            if record is None:
                if command.target_status is InterestStatus.INACTIVE:
                    return InterestUnchanged(
                        interest=Interest(
                            dropId=command.drop_id,
                            status=InterestStatus.INACTIVE,
                            updatedAt=now,
                        ),
                    )
                record = InterestRecord(
                    id=str(uuid4()),
                    user_id=command.user_id,
                    drop_id=command.drop_id,
                    status=command.target_status.value,
                    created_at=now,
                    updated_at=now,
                    version=0,
                )
                session.add(record)
                try:
                    await session.commit()
                except IntegrityError:
                    # 동시에 같은 (user_id, drop_id)를 처음 찜하는 경합 — 상위에서 재조회 후 재시도한다.
                    await session.rollback()
                    return None
                return InterestChanged(interest=_interest_from_record(record))

            if record.status == command.target_status.value:
                return InterestUnchanged(interest=_interest_from_record(record))

            expected_version = record.version
            update_result = await session.execute(
                InterestRecord.__table__.update()
                .where(
                    InterestRecord.id == record.id,
                    InterestRecord.version == expected_version,
                )
                .values(status=command.target_status.value, updated_at=now, version=expected_version + 1),
            )
            if update_result.rowcount == 0:
                await session.rollback()
                return None
            await session.commit()
            return InterestChanged(
                interest=Interest(dropId=record.drop_id, status=command.target_status, updatedAt=now),
            )

    async def list_active_by_user(
        self,
        user_id: UserId,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[Interest], bool]:
        async with self._session_factory() as session:
            query = select(InterestRecord).where(
                InterestRecord.user_id == user_id,
                InterestRecord.status == InterestStatus.ACTIVE.value,
            )
            if cursor is not None:
                query = query.where(InterestRecord.drop_id > cursor)
            query = query.order_by(InterestRecord.drop_id).limit(limit + 1)
            rows = (await session.execute(query)).scalars().all()
            has_next = len(rows) > limit
            selected = rows[:limit]
            return [_interest_from_record(record) for record in selected], has_next

    async def _find(
        self,
        session: AsyncSession,
        user_id: UserId,
        drop_id: DropId,
    ) -> InterestRecord | None:
        result = await session.execute(
            select(InterestRecord).where(
                InterestRecord.user_id == user_id,
                InterestRecord.drop_id == drop_id,
            ),
        )
        return result.scalar_one_or_none()


def _interest_from_record(record: InterestRecord) -> Interest:
    return Interest(
        dropId=record.drop_id,
        status=InterestStatus(record.status),
        updatedAt=record.updated_at,
    )


class DropInterestCounterRecord(Base):
    __tablename__ = "drop_interest_counters"

    drop_id: Mapped[str] = mapped_column(String(64), primary_key=True)
    interest_count: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


class DropViewRecord(Base):
    __tablename__ = "drop_views"

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    drop_id: Mapped[str] = mapped_column(String(64), nullable=False)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    viewed_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


class DropViewRankingRecord(Base):
    __tablename__ = "drop_view_rankings"

    bucket_start: Mapped[datetime] = mapped_column(DateTime(timezone=True), primary_key=True)
    rank: Mapped[int] = mapped_column(Integer, primary_key=True)
    drop_id: Mapped[str] = mapped_column(String(64), nullable=False)
    viewer_count: Mapped[int] = mapped_column(Integer, nullable=False)
    computed_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)


class PostgresDropInterestCounterRepository:
    """DropInterestCounter(AGG.A.07-02) — 리셋 없는 누적 활성 찜 수.

    `INSERT ... ON CONFLICT DO UPDATE SET interest_count = interest_count + delta`로
    원자적 증감하므로 낙관적 잠금(version)이 필요 없다(persistence-design.md 2026-07-14 확인).
    """

    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def increment(self, drop_id: DropId) -> None:
        await self._apply_delta(drop_id, 1)

    async def decrement(self, drop_id: DropId) -> None:
        await self._apply_delta(drop_id, -1)

    async def _apply_delta(self, drop_id: DropId, delta: int) -> None:
        now = datetime.now(UTC)
        async with self._session_factory() as session:
            statement = (
                pg_insert(DropInterestCounterRecord)
                .values(drop_id=drop_id, interest_count=max(delta, 0), updated_at=now)
                .on_conflict_do_update(
                    index_elements=[DropInterestCounterRecord.drop_id],
                    set_={
                        "interest_count": func.greatest(
                            DropInterestCounterRecord.interest_count + delta,
                            0,
                        ),
                        "updated_at": now,
                    },
                )
            )
            await session.execute(statement)
            await session.commit()

    async def get(self, drop_id: DropId) -> DropInterestStats | None:
        async with self._session_factory() as session:
            record = await session.get(DropInterestCounterRecord, drop_id)
            if record is None:
                return None
            return DropInterestStats(dropId=record.drop_id, interestCount=record.interest_count)

    async def list_by_interest_count(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[UpcomingRankingItem], bool]:
        offset = int(cursor) if cursor is not None else 0
        async with self._session_factory() as session:
            query = (
                select(DropInterestCounterRecord)
                .order_by(DropInterestCounterRecord.interest_count.desc(), DropInterestCounterRecord.drop_id)
                .offset(offset)
                .limit(limit + 1)
            )
            rows = (await session.execute(query)).scalars().all()
            has_next = len(rows) > limit
            selected = rows[:limit]
            items = [
                UpcomingRankingItem(dropId=record.drop_id, interestCount=record.interest_count)
                for record in selected
            ]
            return items, has_next


class PostgresDropViewRepository:
    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def record_view(self, drop_id: DropId, user_id: UserId) -> None:
        async with self._session_factory() as session:
            session.add(DropViewRecord(drop_id=drop_id, user_id=user_id, viewed_at=datetime.now(UTC)))
            await session.commit()


class PostgresDropViewRankingRepository:
    """실시간 조회 랭킹 배치 Worker 전용 저장소(`SD.A.0730` "실시간 조회 랭킹 배치 Worker" 참고)."""

    def __init__(self, session_factory: async_sessionmaker[AsyncSession]) -> None:
        self._session_factory = session_factory

    async def compute_and_store_bucket(
        self,
        bucket_start: datetime,
        bucket_end: datetime,
        limit: int,
    ) -> None:
        async with self._session_factory() as session:
            viewer_count = func.count(func.distinct(DropViewRecord.user_id))
            query = (
                select(DropViewRecord.drop_id, viewer_count.label("viewer_count"))
                .where(DropViewRecord.viewed_at >= bucket_start, DropViewRecord.viewed_at < bucket_end)
                .group_by(DropViewRecord.drop_id)
                .order_by(viewer_count.desc(), DropViewRecord.drop_id)
                .limit(limit)
            )
            rows = (await session.execute(query)).all()

            await session.execute(
                DropViewRankingRecord.__table__.delete().where(
                    DropViewRankingRecord.bucket_start == bucket_start,
                ),
            )
            now = datetime.now(UTC)
            for rank, row in enumerate(rows, start=1):
                session.add(
                    DropViewRankingRecord(
                        bucket_start=bucket_start,
                        rank=rank,
                        drop_id=row.drop_id,
                        viewer_count=row.viewer_count,
                        computed_at=now,
                    ),
                )

            retention_cutoff = bucket_start - VIEW_RANKING_RETENTION_WINDOW
            await session.execute(
                DropViewRecord.__table__.delete().where(DropViewRecord.viewed_at < retention_cutoff),
            )
            await session.commit()

    async def get_latest_bucket(
        self,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[TrendingRankingItem], bool, datetime | None]:
        async with self._session_factory() as session:
            max_bucket = (
                await session.execute(select(func.max(DropViewRankingRecord.bucket_start)))
            ).scalar_one_or_none()
            if max_bucket is None:
                return [], False, None

            query = select(DropViewRankingRecord).where(DropViewRankingRecord.bucket_start == max_bucket)
            if cursor is not None:
                query = query.where(DropViewRankingRecord.rank > int(cursor))
            query = query.order_by(DropViewRankingRecord.rank).limit(limit + 1)
            rows = (await session.execute(query)).scalars().all()
            has_next = len(rows) > limit
            selected = rows[:limit]
            items = [
                TrendingRankingItem(dropId=record.drop_id, rank=record.rank, viewerCount=record.viewer_count)
                for record in selected
            ]
            return items, has_next, max_bucket
