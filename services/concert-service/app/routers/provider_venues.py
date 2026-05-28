"""Provider venue APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends, status

from app import schemas
from app.routers.dependencies import venue_service
from app.services import VenueService


router = APIRouter()


@router.post("/provider/venues", status_code=status.HTTP_201_CREATED, response_model=schemas.VenueResponse)
def provider_create_venue(
    request: schemas.VenueCreateRequest,
    concerts: Annotated[VenueService, Depends(venue_service)],
) -> schemas.VenueResponse:
    return concerts.create_venue(request)


@router.get("/provider/venues", response_model=schemas.VenueListResponse)
def provider_list_venues(
    concerts: Annotated[VenueService, Depends(venue_service)],
    limit: int = 20,
) -> schemas.VenueListResponse:
    return concerts.list_venues(limit)
