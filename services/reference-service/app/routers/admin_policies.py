"""Admin policy APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends

from app import schemas
from app.routers.dependencies import open_policy_service, sale_policy_service
from app.services import OpenPolicyService, SalePolicyService


router = APIRouter()


@router.post("/admin/concerts/{concertId}/open-schedule", response_model=schemas.OpenScheduleResponse, include_in_schema=False)
@router.put("/admin/concerts/{concertId}/open-schedule", response_model=schemas.OpenScheduleResponse)
def admin_update_open_schedule(
    concertId: str,
    request: schemas.OpenScheduleUpdateRequest,
    concerts: Annotated[OpenPolicyService, Depends(open_policy_service)],
) -> schemas.OpenScheduleResponse:
    return concerts.update_open_schedule(concertId, request)


@router.get("/admin/concerts/{concertId}/sale-policy", response_model=schemas.SalePolicyResponse)
def admin_get_sale_policy(
    concertId: str,
    concerts: Annotated[SalePolicyService, Depends(sale_policy_service)],
) -> schemas.SalePolicyResponse:
    return concerts.get_sale_policy(concertId)


@router.post("/admin/concerts/{concertId}/sale-policy/approve", response_model=schemas.SalePolicyResponse)
def admin_approve_sale_policy(
    concertId: str,
    concerts: Annotated[SalePolicyService, Depends(sale_policy_service)],
    command: schemas.ApprovalCommand | None = None,
) -> schemas.SalePolicyResponse:
    return concerts.approve_sale_policy(concertId)


@router.post("/admin/concerts/{concertId}/sale-policy/reject", response_model=schemas.SalePolicyResponse)
def admin_reject_sale_policy(
    concertId: str,
    command: schemas.RejectCommand,
    concerts: Annotated[SalePolicyService, Depends(sale_policy_service)],
) -> schemas.SalePolicyResponse:
    return concerts.reject_sale_policy(concertId, command)


@router.post("/admin/concerts/{concertId}/canceled-seat-reopen-policy", response_model=schemas.CanceledSeatReopenPolicyResponse)
def admin_set_canceled_seat_reopen_policy(
    concertId: str,
    request: schemas.CanceledSeatReopenPolicyRequest,
    concerts: Annotated[OpenPolicyService, Depends(open_policy_service)],
) -> schemas.CanceledSeatReopenPolicyResponse:
    return concerts.set_reopen_policy(concertId, request)
