from pydantic import BaseModel, Field

from app.schemas.common import PageInfo


class SeatRowRequest(BaseModel):
    name: str
    seatNumbers: list[str]


class SeatSectionRequest(BaseModel):
    name: str
    rows: list[SeatRowRequest]


class SeatMapRequest(BaseModel):
    sections: list[SeatSectionRequest]


class SeatInventoryItem(BaseModel):
    seatId: str
    status: str
    reason: str | None = None


class SeatInventoryUpdateRequest(BaseModel):
    seats: list[SeatInventoryItem]


class SeatResponse(BaseModel):
    id: str
    performanceId: str
    section: str
    row: str
    number: str
    status: str


class SeatListResponse(BaseModel):
    items: list[SeatResponse]
    page: PageInfo


class SeatMapVenueResponse(BaseModel):
    venueId: str
    name: str


class SeatMapSectionResponse(BaseModel):
    sectionId: str
    name: str
    gradeCode: str
    price: int = Field(ge=0)
    currency: str = "KRW"
    available: bool
    availableCount: int = Field(default=0, ge=0)
    totalCount: int = Field(default=0, ge=0)


class SeatMapSeatResponse(BaseModel):
    seatId: str
    sectionId: str
    row: str
    number: str
    gradeCode: str
    status: str


class SeatMapResponse(BaseModel):
    performanceId: str
    venue: SeatMapVenueResponse
    mapVersion: str
    sections: list[SeatMapSectionResponse]
    seats: list[SeatMapSeatResponse]
    seatLimit: int | None = Field(default=None, ge=0)
    seatOffset: int = Field(default=0, ge=0)
    hasMoreSeats: bool = False


class SeatGradeResponse(BaseModel):
    id: str
    name: str
    price: int = Field(ge=0)
    color: str | None = None


class SeatGradeCreateRequest(BaseModel):
    grades: list[SeatGradeResponse]


class SeatGradeListResponse(BaseModel):
    items: list[SeatGradeResponse]


class HoldSeatRequestCreateRequest(BaseModel):
    type: str
    seatIds: list[str]
    reason: str | None = None


class HoldSeatRequestResponse(BaseModel):
    id: str
    showtimeId: str
    type: str
    seatIds: list[str]
    reason: str | None = None
    status: str
