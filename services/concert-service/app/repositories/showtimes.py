"""Showtime persistence queries."""
from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from app import entities as model


class ShowtimeRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_showtime(self, showtime_id: str) -> model.Showtime | None:
        return self.db.scalar(
            select(model.Showtime)
            .options(selectinload(model.Showtime.seats), selectinload(model.Showtime.venue))
            .where(model.Showtime.id == showtime_id)
        )

    def list_showtimes(self, concert_id: str, limit: int) -> Sequence[model.Showtime]:
        return self.db.scalars(
            select(model.Showtime)
            .where(model.Showtime.concert_id == concert_id)
            .order_by(model.Showtime.starts_at)
            .limit(limit)
        ).all()
