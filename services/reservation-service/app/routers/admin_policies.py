"""Admin reservation policy APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import policy_service
from app.services import ReservationPolicyService


router = APIRouter()


@router.post("/admin/concerts/{concertId}/queue-policy", response_model=schemas.QueuePolicyResponse, include_in_schema=False)
@router.put("/admin/concerts/{concertId}/queue-policy", response_model=schemas.QueuePolicyResponse)
def admin_update_queue_policy(
    concertId: str,
    request: schemas.QueuePolicyUpdateRequest,
    reservations: Annotated[ReservationPolicyService, Depends(policy_service)],
) -> schemas.QueuePolicyResponse:
    return reservations.update_queue_policy(concertId, request)


@router.post("/admin/concerts/{concertId}/traffic-policy", response_model=schemas.TrafficPolicyResponse, include_in_schema=False)
@router.put("/admin/concerts/{concertId}/traffic-policy", response_model=schemas.TrafficPolicyResponse)
def admin_update_traffic_policy(
    concertId: str,
    request: schemas.TrafficPolicyUpdateRequest,
    reservations: Annotated[ReservationPolicyService, Depends(policy_service)],
) -> schemas.TrafficPolicyResponse:
    return reservations.update_traffic_policy(concertId, request)
