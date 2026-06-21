"""Policy entities."""
from datetime import datetime

from sqlalchemy import Boolean, DateTime, ForeignKey, Integer, String, Text
from sqlalchemy.orm import Mapped, mapped_column, relationship
from server.ids import native_uuid

from app.database import Base
from app.entities.concerts import Concert


class SalePolicy(Base):
    __tablename__ = "sale_policies"

    concert_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("concerts.id"), primary_key=True)
    presale_enabled: Mapped[bool] = mapped_column(Boolean, default=False, nullable=False)
    fanclub_verification_required: Mapped[bool] = mapped_column(Boolean, default=False, nullable=False)
    max_tickets_per_user: Mapped[int] = mapped_column(Integer, nullable=False)
    refund_policy: Mapped[str] = mapped_column(Text, nullable=False)
    status: Mapped[str] = mapped_column(String(32), default="submitted", nullable=False)

    concert: Mapped[Concert] = relationship(back_populates="sale_policy")


class OpenRequest(Base):
    __tablename__ = "open_requests"

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    concert_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("concerts.id"), nullable=False, index=True)
    requested_open_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    status: Mapped[str] = mapped_column(String(32), default="requested", nullable=False)
    message: Mapped[str | None] = mapped_column(Text)

    concert: Mapped[Concert] = relationship(back_populates="open_requests")


class CanceledSeatReopenPolicy(Base):
    __tablename__ = "canceled_seat_reopen_policies"

    concert_id: Mapped[str] = mapped_column(native_uuid(), ForeignKey("concerts.id"), primary_key=True)
    enabled: Mapped[bool] = mapped_column(Boolean, nullable=False)
    reopen_delay_seconds: Mapped[int | None] = mapped_column(Integer)
    batch_size: Mapped[int | None] = mapped_column(Integer)
    comment: Mapped[str | None] = mapped_column(Text)

    concert: Mapped[Concert] = relationship(back_populates="reopen_policy")
