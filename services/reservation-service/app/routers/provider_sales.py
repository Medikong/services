"""Provider sales APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import sales_service
from app.services import SalesService


router = APIRouter()


@router.get("/provider/concerts/{concertId}/sales", response_model=schemas.SalesSummaryResponse)
def provider_concert_sales(
    concertId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.SalesSummaryResponse:
    return reservations.sales_summary(concertId)


@router.get("/provider/showtimes/{showtimeId}/sales", response_model=schemas.ShowtimeSalesResponse)
def provider_showtime_sales(
    showtimeId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.ShowtimeSalesResponse:
    return reservations.showtime_sales_summary(showtimeId)
