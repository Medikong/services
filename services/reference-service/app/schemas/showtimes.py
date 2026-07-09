from datetime import date, datetime

from pydantic import BaseModel

from app.schemas.common import PageInfo


class ShowtimeCreateRequest(BaseModel):
    venueId: str
    startsAt: datetime
    endsAt: datetime | None = None


class ShowtimeUpdateRequest(BaseModel):
    startsAt: datetime | None = None
    endsAt: datetime | None = None
    status: str | None = None


class ShowtimeResponse(BaseModel):
    id: str
    concertId: str
    venueId: str
    startsAt: datetime
    endsAt: datetime | None = None
    status: str


class PerformanceResponse(BaseModel):
    id: str
    concertId: str
    venueId: str
    startsAt: datetime
    status: str


class PerformanceListResponse(BaseModel):
    items: list[PerformanceResponse]
    page: PageInfo


class DatePerformanceResponse(BaseModel):
    performanceId: str
    startsAt: datetime
    endsAt: datetime | None = None
    saleStatus: str


class DatePerformanceListResponse(BaseModel):
    concertId: str
    date: date
    timezone: str
    performances: list[DatePerformanceResponse]
