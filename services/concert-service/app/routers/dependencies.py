"""Router dependency factories."""
from typing import Annotated

from fastapi import Depends
from sqlalchemy.orm import Session

from app.dependencies import get_db
from app.services import (
    ConcertCatalogService,
    ConcertReviewService,
    OpenPolicyService,
    ReviewStatusService,
    SalePolicyService,
    SeatService,
    ShowtimeService,
    VenueService,
)


def catalog_service(db: Annotated[Session, Depends(get_db)]) -> ConcertCatalogService:
    return ConcertCatalogService(db)


def venue_service(db: Annotated[Session, Depends(get_db)]) -> VenueService:
    return VenueService(db)


def showtime_service(db: Annotated[Session, Depends(get_db)]) -> ShowtimeService:
    return ShowtimeService(db)


def seat_service(db: Annotated[Session, Depends(get_db)]) -> SeatService:
    return SeatService(db)


def sale_policy_service(db: Annotated[Session, Depends(get_db)]) -> SalePolicyService:
    return SalePolicyService(db)


def open_policy_service(db: Annotated[Session, Depends(get_db)]) -> OpenPolicyService:
    return OpenPolicyService(db)


def review_status_service(db: Annotated[Session, Depends(get_db)]) -> ReviewStatusService:
    return ReviewStatusService(db)


def review_service(db: Annotated[Session, Depends(get_db)]) -> ConcertReviewService:
    return ConcertReviewService(db)
