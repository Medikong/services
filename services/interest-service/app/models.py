from datetime import datetime
from enum import StrEnum, unique
from typing import NewType

from pydantic import BaseModel, ConfigDict

DropId = NewType("DropId", str)
UserId = NewType("UserId", str)


@unique
class InterestStatus(StrEnum):
    ACTIVE = "active"
    INACTIVE = "inactive"


@unique
class UserRole(StrEnum):
    CUSTOMER = "CUSTOMER"
    OPERATOR = "OPERATOR"
    ADMIN = "ADMIN"


class Interest(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    status: InterestStatus
    updatedAt: datetime


class InterestResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: Interest


class InterestListItem(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    addedAt: datetime


class PageInfo(BaseModel):
    model_config = ConfigDict(frozen=True)

    nextCursor: str | None = None
    hasNext: bool


class InterestListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[InterestListItem, ...]
    pageInfo: PageInfo


class UpcomingRankingItem(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    interestCount: int


class UpcomingRankingListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[UpcomingRankingItem, ...]
    pageInfo: PageInfo


class TrendingRankingItem(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    rank: int
    viewerCount: int


class TrendingRankingListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[TrendingRankingItem, ...]
    pageInfo: PageInfo
    bucketStart: datetime | None


class DropInterestStats(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    interestCount: int


class DropInterestStatsResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: DropInterestStats


class HealthResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    timestamp: datetime


class ReadinessResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    status: str
    service: str
    checks: dict[str, str]
    timestamp: datetime
