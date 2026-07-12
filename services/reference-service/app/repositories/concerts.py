"""Concert persistence queries."""
from collections.abc import Sequence
from datetime import datetime

from sqlalchemy import select
from sqlalchemy.orm import Session, selectinload

from app import entities as model


class ConcertRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_concert(self, concert_id: str) -> model.Concert | None:
        return self.db.scalar(
            select(model.Concert)
            .options(
                selectinload(model.Concert.sale_policy),
                selectinload(model.Concert.showtimes).selectinload(model.Showtime.venue),
                selectinload(model.Concert.showtimes).selectinload(model.Showtime.seat_grades),
            )
            .where(model.Concert.id == concert_id)
        )

    def has_concert(self, concert_id: str) -> bool:
        return self.db.scalar(select(model.Concert.id).where(model.Concert.id == concert_id).limit(1)) is not None

    def list_concerts(self, limit: int) -> Sequence[model.Concert]:
        return self.db.scalars(
            select(model.Concert)
            .options(selectinload(model.Concert.showtimes).selectinload(model.Showtime.venue))
            .order_by(model.Concert.created_at.desc())
            .limit(limit)
        ).all()

    def list_recommended_concerts(self, limit: int, before: datetime | None = None) -> Sequence[model.Concert]:
        query = (
            select(model.Concert)
            .options(selectinload(model.Concert.showtimes).selectinload(model.Showtime.venue))
            .order_by(model.Concert.created_at.desc(), model.Concert.id.desc())
            .limit(limit)
        )
        if before is not None:
            query = query.where(model.Concert.created_at < before)
        return self.db.scalars(query).all()
