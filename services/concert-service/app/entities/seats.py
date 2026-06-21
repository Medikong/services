"""Seat entities."""
from sqlalchemy import ForeignKey, Integer, JSON, String, Text, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column, relationship
from server.ids import native_uuid

from app.database import Base
from app.entities.showtimes import Showtime


class Seat(Base):
    __tablename__ = "seats"
    __table_args__ = (UniqueConstraint("showtime_id", "section", "row_label", "number", name="uq_seat_location"),)

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    showtime_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("showtimes.id"), nullable=False, index=True)
    section: Mapped[str] = mapped_column(String(80), nullable=False)
    row_label: Mapped[str] = mapped_column(String(80), nullable=False)
    number: Mapped[str] = mapped_column(String(40), nullable=False)
    status: Mapped[str] = mapped_column(String(32), default="sellable", nullable=False)

    showtime: Mapped[Showtime] = relationship(back_populates="seats")


class SeatGrade(Base):
    __tablename__ = "seat_grades"
    __table_args__ = (UniqueConstraint("showtime_id", "name", name="uq_seat_grade_name"),)

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    showtime_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("showtimes.id"), nullable=False, index=True)
    name: Mapped[str] = mapped_column(String(80), nullable=False)
    price: Mapped[int] = mapped_column(Integer, nullable=False)
    color: Mapped[str | None] = mapped_column(String(40))

    showtime: Mapped[Showtime] = relationship(back_populates="seat_grades")


class HoldSeatRequest(Base):
    __tablename__ = "hold_seat_requests"

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    showtime_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("showtimes.id"), nullable=False, index=True)
    type: Mapped[str] = mapped_column(String(32), nullable=False)
    seat_ids: Mapped[list[str]] = mapped_column(JSON, nullable=False)
    reason: Mapped[str | None] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(32), default="requested", nullable=False)

    showtime: Mapped[Showtime] = relationship(back_populates="hold_requests")
