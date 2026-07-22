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
    # 2026-07-21: 기간편향(오래된 드롭이 누적 찜수만으로 계속 1위) 대응.
    # 정렬 기준이 interestCount 단독에서 전환율(conversionRate) 기반으로 바뀜 — 아래 참고.
    viewCount: int
    conversionRate: float | None


class UpcomingRankingListResponse(BaseModel):
    model_config = ConfigDict(frozen=True)

    data: tuple[UpcomingRankingItem, ...]
    pageInfo: PageInfo


class TrendingRankingItem(BaseModel):
    model_config = ConfigDict(frozen=True)

    dropId: str
    rank: int
    viewerCount: int
    # 2026-07-20: 김정엽 멘토링 피드백(누적 조회수의 기간편향, 찜/조회 신호 결합) 대응.
    # 정렬 기준은 여전히 viewerCount — 아래 두 필드는 참고용으로만 노출한다.
    newInterestCount: int
    conversionRate: float | None


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
