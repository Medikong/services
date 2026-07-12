"""Policy persistence queries."""
from sqlalchemy import select
from sqlalchemy.orm import Session

from app import entities as model


class SalePolicyRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_sale_policy(self, concert_id: str) -> model.SalePolicy | None:
        return self.db.get(model.SalePolicy, concert_id)


class OpenPolicyRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def latest_open_request(self, concert_id: str) -> model.OpenRequest | None:
        return self.db.scalar(
            select(model.OpenRequest)
            .where(model.OpenRequest.concert_id == concert_id)
            .order_by(model.OpenRequest.id.desc())
            .limit(1)
        )
