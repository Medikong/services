"""Provider concert and showtime APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends, status

from app import schemas
from app.dependencies import get_provider_id
from app.routers.dependencies import catalog_service, showtime_service
from app.services import ConcertCatalogService, ShowtimeService


router = APIRouter()


@router.post("/provider/concerts", status_code=status.HTTP_201_CREATED, response_model=schemas.ConcertDraftResponse)
def provider_create_concert(
    request: schemas.ConcertDraftCreateRequest,
    concerts: Annotated[ConcertCatalogService, Depends(catalog_service)],
    provider_id: Annotated[str, Depends(get_provider_id)],
) -> schemas.ConcertDraftResponse:
    return concerts.create_concert(provider_id, request)


@router.patch("/provider/concerts/{concertId}", response_model=schemas.ConcertDraftResponse)
def provider_update_concert(
    concertId: str,
    request: schemas.ConcertUpdateRequest,
    concerts: Annotated[ConcertCatalogService, Depends(catalog_service)],
) -> schemas.ConcertDraftResponse:
    return concerts.update_concert(concertId, request)


@router.post("/provider/concerts/{concertId}/showtimes", status_code=status.HTTP_201_CREATED, response_model=schemas.ShowtimeResponse)
def provider_create_showtime(
    concertId: str,
    request: schemas.ShowtimeCreateRequest,
    concerts: Annotated[ShowtimeService, Depends(showtime_service)],
) -> schemas.ShowtimeResponse:
    return concerts.create_showtime(concertId, request)


@router.patch("/provider/showtimes/{showtimeId}", response_model=schemas.ShowtimeResponse)
def provider_update_showtime(
    showtimeId: str,
    request: schemas.ShowtimeUpdateRequest,
    concerts: Annotated[ShowtimeService, Depends(showtime_service)],
) -> schemas.ShowtimeResponse:
    return concerts.update_showtime(showtimeId, request)
