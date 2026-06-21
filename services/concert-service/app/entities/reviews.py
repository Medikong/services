"""Review entities."""
from datetime import datetime

from sqlalchemy import DateTime, ForeignKey, String, Text
from sqlalchemy.orm import Mapped, mapped_column, relationship
from server.ids import native_uuid

from app.database import Base
from app.entities.concerts import Concert


class ConcertReviewRequest(Base):
    __tablename__ = "concert_review_requests"

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    concert_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("concerts.id"), nullable=False, index=True)
    provider_id: Mapped[str] = mapped_column(String(128), nullable=False, index=True)
    type: Mapped[str] = mapped_column(String(32), nullable=False)
    status: Mapped[str] = mapped_column(String(32), default="pending", nullable=False)
    submitted_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    reviewed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    reason: Mapped[str | None] = mapped_column(Text)

    concert: Mapped[Concert] = relationship()
