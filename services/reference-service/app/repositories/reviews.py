"""Review persistence queries."""
from collections.abc import Sequence

from sqlalchemy import Select, select
from sqlalchemy.orm import Session, selectinload

from app import entities as model


class ConcertReviewRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def list_review_requests(self, limit: int) -> Sequence[model.ConcertReviewRequest]:
        return self.db.scalars(self._review_query().order_by(model.ConcertReviewRequest.submitted_at.desc()).limit(limit)).all()

    def get_review_request(self, request_id: str) -> model.ConcertReviewRequest | None:
        return self.db.scalar(self._review_query().where(model.ConcertReviewRequest.id == request_id))

    @staticmethod
    def _review_query() -> Select[tuple[model.ConcertReviewRequest]]:
        return select(model.ConcertReviewRequest).options(selectinload(model.ConcertReviewRequest.concert))
