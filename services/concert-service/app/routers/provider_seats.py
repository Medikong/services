"""Provider seat APIs."""
from typing import Annotated

from fastapi import APIRouter, Depends, Response, status

from app import schemas
from app.routers.dependencies import seat_service
from app.services import SeatService


router = APIRouter()


@router.post("/provider/showtimes/{showtimeId}/seat-map", status_code=status.HTTP_204_NO_CONTENT)
def provider_upload_seat_map(
    showtimeId: str,
    request: schemas.SeatMapRequest,
    concerts: Annotated[SeatService, Depends(seat_service)],
) -> Response:
    concerts.upload_seat_map(showtimeId, request)
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.patch("/provider/showtimes/{showtimeId}/seat-inventory", status_code=status.HTTP_204_NO_CONTENT)
def provider_update_seat_inventory(
    showtimeId: str,
    request: schemas.SeatInventoryUpdateRequest,
    concerts: Annotated[SeatService, Depends(seat_service)],
) -> Response:
    concerts.update_seat_inventory(showtimeId, request)
    return Response(status_code=status.HTTP_204_NO_CONTENT)


@router.post("/provider/showtimes/{showtimeId}/seat-grades", status_code=status.HTTP_201_CREATED, response_model=schemas.SeatGradeListResponse)
def provider_create_seat_grades(
    showtimeId: str,
    request: schemas.SeatGradeCreateRequest,
    concerts: Annotated[SeatService, Depends(seat_service)],
) -> schemas.SeatGradeListResponse:
    return concerts.create_seat_grades(showtimeId, request)


@router.post("/provider/showtimes/{showtimeId}/hold-seat-requests", status_code=status.HTTP_201_CREATED, response_model=schemas.HoldSeatRequestResponse)
def provider_create_hold_seat_request(
    showtimeId: str,
    request: schemas.HoldSeatRequestCreateRequest,
    concerts: Annotated[SeatService, Depends(seat_service)],
) -> schemas.HoldSeatRequestResponse:
    return concerts.create_hold_request(showtimeId, request)
