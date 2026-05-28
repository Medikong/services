"""Public concert query APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import catalog_service, seat_service, showtime_service
from app.services import ConcertCatalogService, SeatService, ShowtimeService


router = APIRouter()


@router.get("/concerts", response_model=schemas.ConcertListResponse)
def list_concerts(concerts: Annotated[ConcertCatalogService, Depends(catalog_service)], limit: int = 20) -> schemas.ConcertListResponse:
    return concerts.list_public_concerts(limit)


@router.get("/concerts/{id}", response_model=schemas.ConcertResponse)
def get_concert(id: str, concerts: Annotated[ConcertCatalogService, Depends(catalog_service)]) -> schemas.ConcertResponse:
    return concerts.get_public_concert(id)


@router.get("/concerts/{id}/performances", response_model=schemas.PerformanceListResponse)
def list_performances(
    id: str,
    concerts: Annotated[ShowtimeService, Depends(showtime_service)],
    limit: int = 20,
) -> schemas.PerformanceListResponse:
    return concerts.list_performances(id, limit)


@router.get("/performances/{id}/seats", response_model=schemas.SeatListResponse)
def list_performance_seats(
    id: str,
    concerts: Annotated[SeatService, Depends(seat_service)],
    limit: int = 20,
) -> schemas.SeatListResponse:
    return concerts.list_seats(id, limit)
