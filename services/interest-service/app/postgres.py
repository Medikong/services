from datetime import UTC, datetime
from uuid import uuid4

from sqlalchemy import DateTime, Integer, String, UniqueConstraint, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

from app.models import DropId, Interest, InterestStatus, UserId
from app.store import (
    InterestChanged,
    InterestToggleConflict,
    InterestUnchanged,
    ToggleInterestCommand,
    ToggleInterestResult,
)

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
