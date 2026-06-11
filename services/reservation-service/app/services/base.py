from datetime import UTC, datetime
from uuid import uuid4

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app import entities as model
from app.exceptions import ReservationNotFoundError, SalesStateNotFoundError, SeatAlreadyReservedError
from app.repositories import ReservationPolicyRepository, ReservationRepository, SalesRepository


ACTIVE_STATUSES = {"pending", "paid"}


def now_utc() -> datetime:
    return datetime.now(UTC)


def new_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:16]}"


class ReservationDomainService:
    def __init__(self, db: Session) -> None:
        self.db = db
        self.reservations = ReservationRepository(db)
        self.sales = SalesRepository(db)
        self.policies = ReservationPolicyRepository(db)

    def add(self, entity: object) -> object:
        self.db.add(entity)
        return entity

    def commit(self) -> None:
        self.db.commit()

    def _reservation(self, reservation_id: str) -> model.Reservation:
        reservation = self.reservations.get_reservation(reservation_id)
        if reservation is None:
            raise ReservationNotFoundError(reservation_id)
        return reservation

    def _sales_state(self, concert_id: str) -> model.SalesState:
        state = self.sales.get_sales_state(concert_id)
        if state is None:
            raise SalesStateNotFoundError(concert_id)
        return state

    def _commit_or_reservation_conflict(self, seat_id: str | None = None) -> None:
        try:
            self.commit()
        except IntegrityError as exc:
            self.db.rollback()
            raise SeatAlreadyReservedError(seat_id) from exc
