"""Venue persistence queries."""
from collections.abc import Sequence

from sqlalchemy import select
from sqlalchemy.orm import Session

from app import entities as model


class VenueRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_venue(self, venue_id: str) -> model.Venue | None:
        return self.db.get(model.Venue, venue_id)

    def list_venues(self, limit: int) -> Sequence[model.Venue]:
        return self.db.scalars(select(model.Venue).order_by(model.Venue.name).limit(limit)).all()
