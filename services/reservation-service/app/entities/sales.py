"""Sales entities."""
from datetime import datetime

from sqlalchemy import DateTime, Integer, String
from sqlalchemy.orm import Mapped, mapped_column

from app.database import Base


class SalesState(Base):
    __tablename__ = "sales_states"

    concert_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    sales_status: Mapped[str] = mapped_column(String(32), default="ready", nullable=False)
    total_seats: Mapped[int] = mapped_column(Integer, default=0, nullable=False)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
