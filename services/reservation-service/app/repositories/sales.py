"""Sales persistence queries."""
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app import entities as model


class SalesRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_sales_state(self, concert_id: str) -> model.SalesState | None:
        return self.db.get(model.SalesState, concert_id)

    def get_or_create_sales_state(self, concert_id: str, updated_at) -> model.SalesState:
        state = self.get_sales_state(concert_id)
        if state is None:
            state = model.SalesState(concert_id=concert_id, sales_status="ready", total_seats=0, updated_at=updated_at)
            self.db.add(state)
        return state

    def reservation_counts_for_concert(self, concert_id: str) -> tuple[int, int]:
        rows = self.db.execute(
            select(model.Reservation.status, func.count())
            .where(model.Reservation.concert_id == concert_id)
            .group_by(model.Reservation.status)
        ).all()
        counts = {status: count for status, count in rows}
        return int(counts.get("paid", 0)), int(counts.get("pending", 0))

    def reservation_counts_for_showtime(self, showtime_id: str) -> tuple[int, int]:
        rows = self.db.execute(
            select(model.Reservation.status, func.count())
            .where(model.Reservation.showtime_id == showtime_id)
            .group_by(model.Reservation.status)
        ).all()
        counts = {status: count for status, count in rows}
        return int(counts.get("paid", 0)), int(counts.get("pending", 0))
