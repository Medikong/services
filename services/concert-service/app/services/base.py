from datetime import UTC, datetime
from uuid import uuid4

from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app import entities as model
from app.exceptions import ConflictError, NotFoundError
from app.repositories import (
    ConcertRepository,
    ConcertReviewRepository,
    OpenPolicyRepository,
    SalePolicyRepository,
    SeatRepository,
    ShowtimeRepository,
    VenueRepository,
)


def now_utc() -> datetime:
    return datetime.now(UTC)


def new_id(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:16]}"


class ConcertDomainService:
    def __init__(self, db: Session) -> None:
        self.db = db
        self.concerts = ConcertRepository(db)
        self.venues = VenueRepository(db)
        self.showtimes = ShowtimeRepository(db)
        self.seats = SeatRepository(db)
        self.sale_policies = SalePolicyRepository(db)
        self.open_policies = OpenPolicyRepository(db)
        self.reviews = ConcertReviewRepository(db)

    def add(self, entity: object) -> object:
        self.db.add(entity)
        return entity

    def commit(self) -> None:
        self.db.commit()

    def _concert(self, concert_id: str) -> model.Concert:
        concert = self.concerts.get_concert(concert_id)
        if concert is None:
            raise NotFoundError("concert", concert_id)
        return concert

    def _venue(self, venue_id: str) -> model.Venue:
        venue = self.venues.get_venue(venue_id)
        if venue is None:
            raise NotFoundError("venue", venue_id)
        return venue

    def _showtime(self, showtime_id: str) -> model.Showtime:
        showtime = self.showtimes.get_showtime(showtime_id)
        if showtime is None:
            raise NotFoundError("showtime", showtime_id)
        return showtime

    def _sale_policy(self, concert_id: str) -> model.SalePolicy:
        self._concert(concert_id)
        policy = self.sale_policies.get_sale_policy(concert_id)
        if policy is None:
            raise NotFoundError("sale_policy", concert_id)
        return policy

    def _review_request(self, request_id: str) -> model.ConcertReviewRequest:
        request = self.reviews.get_review_request(request_id)
        if request is None:
            raise NotFoundError("review_request", request_id)
        return request

    def _commit_or_conflict(self, code: str, message: str) -> None:
        try:
            self.commit()
        except IntegrityError as exc:
            self.db.rollback()
            raise ConflictError(code, message) from exc
