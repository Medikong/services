"""Seat persistence queries."""
from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session

from app import entities as model


class SeatRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def list_seats(self, showtime_id: str, limit: int) -> Sequence[model.Seat]:
        return self.db.scalars(
            select(model.Seat)
            .where(model.Seat.showtime_id == showtime_id)
            .order_by(model.Seat.section, model.Seat.row_label, model.Seat.number)
            .limit(limit)
        ).all()

    def get_seat(self, seat_id: str) -> model.Seat | None:
        return self.db.get(model.Seat, seat_id)

    def delete_showtime_seats(self, showtime_id: str) -> None:
        for seat in self.list_seats(showtime_id, limit=10000):
            self.db.delete(seat)
