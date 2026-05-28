from datetime import datetime

from pydantic import BaseModel, Field

from app.schemas.common import PageInfo
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
