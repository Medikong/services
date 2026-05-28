"""Provider policy APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends, status

from app import schemas
from app.routers.dependencies import open_policy_service, review_status_service, sale_policy_service
from app.services import OpenPolicyService, ReviewStatusService, SalePolicyService


router = APIRouter()


@router.post("/provider/concerts/{concertId}/sale-policy", response_model=schemas.SalePolicyResponse, include_in_schema=False)
@router.put("/provider/concerts/{concertId}/sale-policy", response_model=schemas.SalePolicyResponse)
def provider_update_sale_policy(
    concertId: str,
    request: schemas.SalePolicyUpdateRequest,
    concerts: Annotated[SalePolicyService, Depends(sale_policy_service)],
) -> schemas.SalePolicyResponse:
    return concerts.update_sale_policy(concertId, request)


@router.post("/provider/concerts/{concertId}/open-request", status_code=status.HTTP_202_ACCEPTED, response_model=schemas.OpenRequestResponse)
def provider_submit_open_request(
    concertId: str,
    request: schemas.OpenRequestCreateRequest,
    concerts: Annotated[OpenPolicyService, Depends(open_policy_service)],
) -> schemas.OpenRequestResponse:
    return concerts.submit_open_request(concertId, request)


@router.get("/provider/concerts/{concertId}/review-status", response_model=schemas.ReviewStatusResponse)
def provider_get_review_status(
    concertId: str,
    concerts: Annotated[ReviewStatusService, Depends(review_status_service)],
) -> schemas.ReviewStatusResponse:
    return concerts.review_status(concertId)
