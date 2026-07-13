from datetime import UTC, datetime
from typing import Final
from uuid import uuid4

from pydantic import BaseModel, ConfigDict

from app.models import DropId, UserId

# interest-service 내부 이벤트다(EVT.A.07-01/-02). 아직 다른 서비스가 구독하지 않으므로
# packages/contracts(팀 공유 계약)에는 두지 않는다 — 필요해지면 그때 협의 후 승격한다.
INTEREST_ADDED_TOPIC: Final = "interest.added"
INTEREST_REMOVED_TOPIC: Final = "interest.removed"

PRODUCER_NAME: Final = "interest-service"


class InterestAddedEvent(BaseModel):
    model_config = ConfigDict(frozen=True)

    eventId: str
    userId: UserId
    dropId: DropId
    occurredAt: datetime
    producer: str = PRODUCER_NAME


class InterestRemovedEvent(BaseModel):
    model_config = ConfigDict(frozen=True)

    eventId: str
    userId: UserId
    dropId: DropId
    occurredAt: datetime
    producer: str = PRODUCER_NAME


def interest_added_event(user_id: UserId, drop_id: DropId) -> InterestAddedEvent:
    return InterestAddedEvent(
        eventId=f"evt-{uuid4().hex}",
        userId=user_id,
        dropId=drop_id,
        occurredAt=datetime.now(UTC),
    )


def interest_removed_event(user_id: UserId, drop_id: DropId) -> InterestRemovedEvent:
    return InterestRemovedEvent(
        eventId=f"evt-{uuid4().hex}",
        userId=user_id,
        dropId=drop_id,
        occurredAt=datetime.now(UTC),
    )
