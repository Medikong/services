from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session

from app import entities as model


class ReservationRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def add(self, entity: object) -> object:
        self.db.add(entity)
        return entity

    def get_reservation(self, reservation_id: str) -> model.Reservation | None:
        return self.db.get(model.Reservation, reservation_id)

    def find_active_reservation(self, performance_id: str, seat_id: str) -> model.Reservation | None:
        return self.db.scalar(
            select(model.Reservation).where(
                model.Reservation.performance_id == performance_id,
                model.Reservation.seat_id == seat_id,
                model.Reservation.status.in_(("pending", "paid")),
            )
        )

    def list_user_reservations(self, user_id: str, limit: int) -> Sequence[model.Reservation]:
        return self.db.scalars(
            select(model.Reservation)
            .where(model.Reservation.user_id == user_id)
            .order_by(model.Reservation.created_at.desc())
            .limit(limit)
        ).all()

    def commit(self) -> None:
        self.db.commit()

    def rollback(self) -> None:
        self.db.rollback()
