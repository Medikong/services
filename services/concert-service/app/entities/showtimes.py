"""Showtime entities."""
from datetime import datetime

from sqlalchemy import DateTime, ForeignKey, String
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.database import Base
from app.entities.concerts import Concert
from app.entities.venues import Venue


class Showtime(Base):
    __tablename__ = "showtimes"

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    concert_id: Mapped[str] = mapped_column(ForeignKey("concerts.id"), nullable=False, index=True)
    venue_id: Mapped[str] = mapped_column(ForeignKey("venues.id"), nullable=False, index=True)
    starts_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    ends_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    status: Mapped[str] = mapped_column(String(32), default="draft", nullable=False)

    concert: Mapped[Concert] = relationship(back_populates="showtimes")
    venue: Mapped[Venue] = relationship()
    seats: Mapped[list["Seat"]] = relationship(back_populates="showtime", cascade="all, delete-orphan")
    seat_grades: Mapped[list["SeatGrade"]] = relationship(back_populates="showtime", cascade="all, delete-orphan")
    hold_requests: Mapped[list["HoldSeatRequest"]] = relationship(back_populates="showtime", cascade="all, delete-orphan")
