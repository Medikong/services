"""Admin review APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import review_service
from app.services import ConcertReviewService


router = APIRouter()


@router.get("/admin/concert-requests", response_model=schemas.ConcertReviewRequestListResponse)
def admin_list_review_requests(
    concerts: Annotated[ConcertReviewService, Depends(review_service)],
    limit: int = 20,
) -> schemas.ConcertReviewRequestListResponse:
    return concerts.list_review_requests(limit)


@router.get("/admin/concert-requests/{requestId}", response_model=schemas.ConcertReviewRequestResponse)
def admin_get_review_request(
    requestId: str,
    concerts: Annotated[ConcertReviewService, Depends(review_service)],
) -> schemas.ConcertReviewRequestResponse:
    return concerts.get_review_request(requestId)


@router.post("/admin/concert-requests/{requestId}/approve", response_model=schemas.ConcertReviewRequestResponse)
def admin_approve_review_request(
    requestId: str,
    concerts: Annotated[ConcertReviewService, Depends(review_service)],
    command: schemas.ApprovalCommand | None = None,
) -> schemas.ConcertReviewRequestResponse:
    return concerts.approve_review_request(requestId)


@router.post("/admin/concert-requests/{requestId}/reject", response_model=schemas.ConcertReviewRequestResponse)
def admin_reject_review_request(
    requestId: str,
    command: schemas.RejectCommand,
    concerts: Annotated[ConcertReviewService, Depends(review_service)],
) -> schemas.ConcertReviewRequestResponse:
    return concerts.reject_review_request(requestId, command)
