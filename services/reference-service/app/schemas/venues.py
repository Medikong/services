from pydantic import BaseModel, Field

from app.schemas.common import PageInfo


class VenueCreateRequest(BaseModel):
    name: str
    address: str | None = None
    totalSeats: int = Field(default=0, ge=0)


class VenueResponse(BaseModel):
    id: str
    name: str
    address: str | None = None


class VenueListResponse(BaseModel):
    items: list[VenueResponse]
    page: PageInfo
