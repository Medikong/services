from datetime import datetime

from sqlalchemy import JSON, DateTime, Index, Integer, String, UniqueConstraint, func
from sqlalchemy.orm import Mapped, mapped_column
from server.ids import native_uuid

from app.database import Base


class Payment(Base):
    __tablename__ = "payments"
    __table_args__ = (
        UniqueConstraint("user_id", "idempotency_key", name="uq_payments_user_idempotency_key"),
        Index("ix_payments_concert_status", "concert_id", "status"),
    )

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    reservation_id: Mapped[str] = mapped_column(native_uuid(), index=True, nullable=False)
    concert_id: Mapped[str] = mapped_column(native_uuid(), nullable=False)
    user_id: Mapped[str] = mapped_column(String(120), index=True, nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    method: Mapped[str] = mapped_column(String(30), nullable=False)
    status: Mapped[str] = mapped_column(String(30), index=True, nullable=False)
    idempotency_key: Mapped[str | None] = mapped_column(String(128), nullable=True)
    approved_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    @property
    def reservationId(self) -> str:
        return self.reservation_id

    @property
    def concertId(self) -> str:
        return self.concert_id

    @property
    def approvedAt(self) -> datetime | None:
        return self.approved_at

    @property
    def createdAt(self) -> datetime:
        return self.created_at


class PaymentEvent(Base):
    __tablename__ = "payment_events"

    id: Mapped[str] = mapped_column(native_uuid(), primary_key=True)
    event_type: Mapped[str] = mapped_column(String(80), index=True, nullable=False)
    payment_id: Mapped[str] = mapped_column(native_uuid(), index=True, nullable=False)
    payload: Mapped[dict] = mapped_column(JSON, nullable=False)
    trace_context: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    publish_status: Mapped[str] = mapped_column(String(20), index=True, nullable=False, default="pending")
    published_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    publish_attempts: Mapped[int] = mapped_column(Integer, nullable=False, default=0)
    last_publish_error: Mapped[str | None] = mapped_column(String(500), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
