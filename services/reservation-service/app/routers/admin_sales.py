"""Admin sales APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import sales_service
from app.services import SalesService


router = APIRouter()


@router.post("/admin/concerts/{concertId}/sales/start", response_model=schemas.SalesCommandResponse)
def admin_start_sales(
    concertId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.SalesCommandResponse:
    return reservations.start_sales(concertId)


@router.post("/admin/concerts/{concertId}/sales/pause", response_model=schemas.SalesCommandResponse)
def admin_pause_sales(
    concertId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.SalesCommandResponse:
    return reservations.pause_sales(concertId)


@router.post("/admin/concerts/{concertId}/sales/resume", response_model=schemas.SalesCommandResponse)
def admin_resume_sales(
    concertId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.SalesCommandResponse:
    return reservations.resume_sales(concertId)


@router.get("/admin/concerts/{concertId}/sales", response_model=schemas.SalesSummaryResponse)
def admin_get_sales(
    concertId: str,
    reservations: Annotated[SalesService, Depends(sales_service)],
) -> schemas.SalesSummaryResponse:
    return reservations.sales_summary(concertId)
