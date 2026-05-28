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
