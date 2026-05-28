from datetime import datetime

from pydantic import BaseModel, Field


class SalesSummaryResponse(BaseModel):
    concertId: str
    salesStatus: str
    totalSeats: int = Field(ge=0)
    soldSeats: int = Field(ge=0)
    reservedSeats: int = Field(ge=0)
    grossAmount: int = Field(ge=0)
    updatedAt: datetime


class ShowtimeSalesResponse(BaseModel):
    showtimeId: str
    totalSeats: int = Field(ge=0)
    availableSeats: int = Field(ge=0)
    soldSeats: int = Field(ge=0)
    reservedSeats: int = Field(ge=0)
    grossAmount: int = Field(ge=0)
    updatedAt: datetime


class SalesCommandResponse(BaseModel):
    concertId: str
    salesStatus: str
    changedAt: datetime
