"""Concert entities."""
from datetime import datetime

from sqlalchemy import DateTime, Integer, String, Text
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.database import Base


class Concert(Base):
    __tablename__ = "concerts"

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    provider_id: Mapped[str] = mapped_column(String(128), nullable=False, index=True)
    title: Mapped[str] = mapped_column(String(200), nullable=False)
    description: Mapped[str | None] = mapped_column(Text)
    poster_url: Mapped[str | None] = mapped_column(String(1000))
    age_rating: Mapped[str] = mapped_column(String(40), nullable=False)
    running_minutes: Mapped[int] = mapped_column(Integer, nullable=False)
    status: Mapped[str] = mapped_column(String(32), default="draft", nullable=False, index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    updated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    opens_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    open_schedule_status: Mapped[str | None] = mapped_column(String(32))
    last_reviewed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    review_reason: Mapped[str | None] = mapped_column(Text)

    showtimes: Mapped[list["Showtime"]] = relationship(back_populates="concert", cascade="all, delete-orphan")
    sale_policy: Mapped["SalePolicy | None"] = relationship(back_populates="concert", cascade="all, delete-orphan")
    open_requests: Mapped[list["OpenRequest"]] = relationship(back_populates="concert", cascade="all, delete-orphan")
    reopen_policy: Mapped["CanceledSeatReopenPolicy | None"] = relationship(
        back_populates="concert",
        cascade="all, delete-orphan",
    )
