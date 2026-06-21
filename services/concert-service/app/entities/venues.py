"""Venue entities."""
from sqlalchemy import Integer, String
from sqlalchemy.orm import Mapped, mapped_column
from server.ids import native_uuid

from app.database import Base


class Venue(Base):
    __tablename__ = "venues"

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    name: Mapped[str] = mapped_column(String(200), nullable=False)
    address: Mapped[str | None] = mapped_column(String(500))
    total_seats: Mapped[int] = mapped_column(Integer, default=0, nullable=False)
