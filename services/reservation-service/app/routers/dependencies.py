"""Router dependency factories."""
from typing import Annotated

from fastapi import Depends
from sqlalchemy.orm import Session

from app.dependencies import get_db
from app.services import ReservationCommandService, ReservationPolicyService, ReservationQueryService, SalesService


def reservation_command_service(db: Annotated[Session, Depends(get_db)]) -> ReservationCommandService:
    return ReservationCommandService(db)


def reservation_query_service(db: Annotated[Session, Depends(get_db)]) -> ReservationQueryService:
    return ReservationQueryService(db)


def sales_service(db: Annotated[Session, Depends(get_db)]) -> SalesService:
    return SalesService(db)


def policy_service(db: Annotated[Session, Depends(get_db)]) -> ReservationPolicyService:
    return ReservationPolicyService(db)
