"""Reservation entities."""
from datetime import datetime

from sqlalchemy import DateTime, String, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column
from server.ids import native_uuid

from app.database import Base


class Reservation(Base):
    __tablename__ = "reservations"
    __table_args__ = (UniqueConstraint("active_seat_key", name="uq_active_seat_reservation"),)

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(128), nullable=False, index=True)
    concert_id: Mapped[str] = mapped_column(native_uuid(), nullable=False, index=True)
    showtime_id: Mapped[str] = mapped_column(native_uuid(), nullable=False, index=True)
    performance_id: Mapped[str] = mapped_column(native_uuid(), nullable=False, index=True)
    seat_id: Mapped[str] = mapped_column(native_uuid(), nullable=False, index=True)
    status: Mapped[str] = mapped_column(String(32), nullable=False, index=True)
    active_seat_key: Mapped[str | None] = mapped_column(String(300), nullable=True)
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
