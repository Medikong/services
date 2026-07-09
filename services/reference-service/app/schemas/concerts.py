from datetime import date, datetime

from pydantic import BaseModel, Field

from app.schemas.common import CursorPageInfo, PageInfo
from app.schemas.venues import VenueResponse


class ConcertDraftCreateRequest(BaseModel):
    title: str
    description: str | None = None
    posterUrl: str | None = None
    ageRating: str
    runningMinutes: int = Field(ge=1)


class ConcertUpdateRequest(BaseModel):
    title: str | None = None
    description: str | None = None
    posterUrl: str | None = None
    ageRating: str | None = None
    runningMinutes: int | None = Field(default=None, ge=1)


class ConcertDraftResponse(BaseModel):
    id: str
    providerId: str
    title: str
    description: str | None = None
    posterUrl: str | None = None
    ageRating: str
    runningMinutes: int
    status: str
    createdAt: datetime
    updatedAt: datetime | None = None


class ConcertResponse(BaseModel):
    id: str
    title: str
    venue: VenueResponse
    startsAt: datetime
    status: str


class ConcertListResponse(BaseModel):
    items: list[ConcertResponse]
    page: PageInfo


class RecommendedVenueSummary(BaseModel):
    venueId: str
    name: str
    city: str | None = None


class PerformancePeriodResponse(BaseModel):
    startDate: date
    endDate: date


class RecommendedConcertResponse(BaseModel):
    concertId: str
    title: str
    posterImageUrl: str | None = None
    venue: RecommendedVenueSummary
    performancePeriod: PerformancePeriodResponse
    saleBadge: str
    createdAt: datetime


class RecommendedConcertListResponse(BaseModel):
    items: list[RecommendedConcertResponse]
    page: CursorPageInfo


class ConcertDetailVenueResponse(BaseModel):
    venueId: str
    name: str
    address: str | None = None
    city: str | None = None


class BookingPeriodResponse(BaseModel):
    openAt: datetime | None = None
    closeAt: datetime | None = None


class PriceBandResponse(BaseModel):
    gradeCode: str
    gradeName: str
    price: int = Field(ge=0)
    currency: str = "KRW"


class PurchaseLimitResponse(BaseModel):
    maxTicketsPerUser: int = Field(ge=1)
    maxTicketsPerPerformance: int = Field(ge=1)


class ConcertDetailResponse(BaseModel):
    concertId: str
    title: str
    description: str | None = None
    posterImageUrl: str | None = None
    venue: ConcertDetailVenueResponse
    performancePeriod: PerformancePeriodResponse
    bookingPeriod: BookingPeriodResponse
    priceBands: list[PriceBandResponse]
    purchaseLimit: PurchaseLimitResponse
    notices: list[str]
    saleStatus: str


class CalendarDayResponse(BaseModel):
    date: date
    bookable: bool


class ConcertCalendarResponse(BaseModel):
    concertId: str
    yearMonth: str
    timezone: str
    days: list[CalendarDayResponse]
