"""Reservation policy persistence queries."""
from sqlalchemy.orm import Session

from app import entities as model


class ReservationPolicyRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_queue_policy(self, concert_id: str) -> model.QueuePolicy | None:
        return self.db.get(model.QueuePolicy, concert_id)

    def get_traffic_policy(self, concert_id: str) -> model.TrafficPolicy | None:
        return self.db.get(model.TrafficPolicy, concert_id)

    def save_queue_policy(self, policy: model.QueuePolicy) -> model.QueuePolicy:
        return self.db.merge(policy)

    def save_traffic_policy(self, policy: model.TrafficPolicy) -> model.TrafficPolicy:
        return self.db.merge(policy)
