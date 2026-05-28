"""Concert persistence queries."""
from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from app import entities as model


class ConcertRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_concert(self, concert_id: str) -> model.Concert | None:
        return self.db.scalar(
            select(model.Concert)
            .options(selectinload(model.Concert.showtimes).selectinload(model.Showtime.venue))
            .where(model.Concert.id == concert_id)
        )

    def list_concerts(self, limit: int) -> Sequence[model.Concert]:
        return self.db.scalars(
            select(model.Concert)
            .options(selectinload(model.Concert.showtimes).selectinload(model.Showtime.venue))
            .order_by(model.Concert.created_at.desc())
            .limit(limit)
        ).all()
