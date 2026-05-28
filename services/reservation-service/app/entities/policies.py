"""Reservation policy entities."""
from sqlalchemy import Boolean, Integer, String
from sqlalchemy.orm import Mapped, mapped_column

from app.database import Base


class QueuePolicy(Base):
    __tablename__ = "queue_policies"

    concert_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False)
    max_entrants_per_minute: Mapped[int | None] = mapped_column(Integer)
    waiting_room_url: Mapped[str | None] = mapped_column(String(1000))


class TrafficPolicy(Base):
    __tablename__ = "traffic_policies"

    concert_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    macro_protection_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False)
    max_requests_per_user_per_minute: Mapped[int | None] = mapped_column(Integer)
    block_suspicious_traffic: Mapped[bool] = mapped_column(Boolean, default=True, nullable=False)
