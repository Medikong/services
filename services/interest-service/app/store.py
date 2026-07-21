from collections import defaultdict
from dataclasses import dataclass
from datetime import UTC, datetime
from uuid import uuid4

from app.models import DropId, Interest, InterestStatus, UserId


@dataclass(frozen=True, slots=True)
class ToggleInterestCommand:
    user_id: UserId
    drop_id: DropId
    target_status: InterestStatus


@dataclass(frozen=True, slots=True)
class InterestChanged:
    interest: Interest


@dataclass(frozen=True, slots=True)
class InterestUnchanged:
    interest: Interest


@dataclass(frozen=True, slots=True)
class InterestToggleConflict:
    pass


type ToggleInterestResult = InterestChanged | InterestUnchanged | InterestToggleConflict


@dataclass(slots=True)
class _InterestRecord:
    id: str
    user_id: UserId
    drop_id: DropId
    status: InterestStatus
    created_at: datetime
    updated_at: datetime
    version: int = 0


class InterestStore:
    """In-memory InterestRepository 구현. DATABASE_URL이 없을 때(로컬/테스트) 쓰인다."""

    def __init__(self) -> None:
        self._records: dict[tuple[UserId, DropId], _InterestRecord] = {}

    async def upsert_status(self, command: ToggleInterestCommand) -> ToggleInterestResult:
        key = (command.user_id, command.drop_id)
        existing = self._records.get(key)
        now = datetime.now(UTC)

        if existing is None:
            if command.target_status is InterestStatus.INACTIVE:
                return InterestUnchanged(
                    interest=Interest(
                        dropId=command.drop_id,
                        status=InterestStatus.INACTIVE,
                        updatedAt=now,
                    ),
                )
            record = _InterestRecord(
                id=str(uuid4()),
                user_id=command.user_id,
                drop_id=command.drop_id,
                status=command.target_status,
                created_at=now,
                updated_at=now,
            )
            self._records[key] = record
            return InterestChanged(interest=_interest_from_record(record))

        if existing.status is command.target_status:
            return InterestUnchanged(interest=_interest_from_record(existing))

        existing.status = command.target_status
        existing.updated_at = now
        existing.version += 1
        return InterestChanged(interest=_interest_from_record(existing))

    async def list_active_by_user(
        self,
        user_id: UserId,
        limit: int,
        cursor: str | None,
    ) -> tuple[list[Interest], bool]:
        active = sorted(
            (
                record
                for record in self._records.values()
                if record.user_id == user_id and record.status is InterestStatus.ACTIVE
            ),
            key=lambda record: record.drop_id,
        )
        start = _start_index_after(active, cursor)
        selected = active[start : start + limit]
        has_next = start + limit < len(active)
        return [_interest_from_record(record) for record in selected], has_next

    async def count_active_updated_in_window(
        self,
        start: datetime,
        end: datetime,
    ) -> dict[DropId, int]:
        counts: dict[DropId, int] = defaultdict(int)
        for record in self._records.values():
            if record.status is InterestStatus.ACTIVE and start <= record.updated_at < end:
                counts[record.drop_id] += 1
        return dict(counts)


def _interest_from_record(record: _InterestRecord) -> Interest:
    return Interest(dropId=record.drop_id, status=record.status, updatedAt=record.updated_at)


def _start_index_after(records: list[_InterestRecord], cursor: str | None) -> int:
    if cursor is None:
        return 0
    for index, record in enumerate(records):
        if record.drop_id == cursor:
            return index + 1
    return len(records)
