from typing import Annotated

from fastapi import APIRouter, Depends, status

from app import schemas
from app.dependencies import get_user_id
from app.routers.dependencies import reservation_command_service, reservation_query_service
from app.services import ReservationCommandService, ReservationQueryService


router = APIRouter()


@router.post("/reservations", status_code=status.HTTP_201_CREATED, response_model=schemas.ReservationResponse)
def create_reservation(
    request: schemas.CreateReservationRequest,
    reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)],
    user_id: Annotated[str, Depends(get_user_id)],
) -> schemas.ReservationResponse:
    return reservations.create_reservation(user_id, request)


@router.get("/reservations/me", response_model=schemas.ReservationListResponse)
def list_my_reservations(
    reservations: Annotated[ReservationQueryService, Depends(reservation_query_service)],
    user_id: Annotated[str, Depends(get_user_id)],
    limit: int = 20,
) -> schemas.ReservationListResponse:
    return reservations.list_my_reservations(user_id, limit)


@router.get("/reservations/{id}", response_model=schemas.ReservationResponse)
def get_reservation(id: str, reservations: Annotated[ReservationQueryService, Depends(reservation_query_service)]) -> schemas.ReservationResponse:
    return reservations.get_reservation(id)


@router.post("/reservations/{id}/cancel", response_model=schemas.ReservationResponse)
def cancel_reservation(id: str, reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)]) -> schemas.ReservationResponse:
    return reservations.cancel_reservation(id)


@router.post("/reservations/{id}/expire", response_model=schemas.ReservationResponse)
def expire_reservation(id: str, reservations: Annotated[ReservationCommandService, Depends(reservation_command_service)]) -> schemas.ReservationResponse:
    return reservations.expire_reservation(id)
