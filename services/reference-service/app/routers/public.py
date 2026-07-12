"""Public concert query APIs."""
from datetime import date
from typing import Annotated

from fastapi import APIRouter, Depends, Query

from app import schemas
from app.routers.dependencies import catalog_service, seat_service, showtime_service
from app.services import ConcertCatalogService, SeatService, ShowtimeService


router = APIRouter()


@router.get("/concerts", response_model=schemas.ConcertListResponse)
def list_concerts(concerts: Annotated[ConcertCatalogService, Depends(catalog_service)], limit: int = 20) -> schemas.ConcertListResponse:
    return concerts.list_public_concerts(limit)


@router.get("/concerts/recommended", response_model=schemas.RecommendedConcertListResponse)
def list_recommended_concerts(
    concerts: Annotated[ConcertCatalogService, Depends(catalog_service)],
    sort: str = "latest",
    cursor: str | None = None,
    limit: int = Query(default=10, ge=1),
) -> schemas.RecommendedConcertListResponse:
    return concerts.list_recommended_concerts(sort=sort, cursor=cursor, limit=limit)


@router.get("/concerts/{id}", response_model=schemas.ConcertDetailResponse)
def get_concert(id: str, concerts: Annotated[ConcertCatalogService, Depends(catalog_service)]) -> schemas.ConcertDetailResponse:
    return concerts.get_concert_detail(id)


@router.get("/concerts/{id}/calendar", response_model=schemas.ConcertCalendarResponse)
def get_concert_calendar(
    id: str,
    concerts: Annotated[ConcertCatalogService, Depends(catalog_service)],
    yearMonth: str | None = None,
) -> schemas.ConcertCalendarResponse:
    return concerts.get_monthly_availability(id, yearMonth)


@router.get("/concerts/{id}/dates/{selected_date}/performances", response_model=schemas.DatePerformanceListResponse)
def list_date_performances(
    id: str,
    selected_date: date,
    concerts: Annotated[ShowtimeService, Depends(showtime_service)],
) -> schemas.DatePerformanceListResponse:
    return concerts.list_performances_by_date(id, selected_date)


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


@router.get("/performances/{id}/seat-map", response_model=schemas.SeatMapResponse)
def get_performance_seat_map(
    id: str,
    concerts: Annotated[SeatService, Depends(seat_service)],
    limit: int = Query(default=200, ge=0, le=500),
    offset: int = Query(default=0, ge=0),
    sectionId: str | None = None,
) -> schemas.SeatMapResponse:
    return concerts.get_seat_map(id, limit=limit, offset=offset, section_id=sectionId)
