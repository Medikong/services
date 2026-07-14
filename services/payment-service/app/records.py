from datetime import datetime

from sqlalchemy import (
    CheckConstraint,
    DateTime,
    ForeignKeyConstraint,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
)
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column

type JsonValue = str | int | bool | None | list["JsonValue"] | dict[str, "JsonValue"]


class Base(DeclarativeBase):
    pass


class KnownOrderRecord(Base):
    __tablename__ = "known_orders"
    __table_args__ = (
        CheckConstraint("amount >= 0", name="ck_known_orders_amount_nonnegative"),
    )

    order_id: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), nullable=False
    )


class PaymentRecord(Base):
    __tablename__ = "payments"
    __table_args__ = (
        CheckConstraint("amount >= 0", name="ck_payments_amount_nonnegative"),
        CheckConstraint("method = 'MOCK_CARD'", name="ck_payments_method"),
        CheckConstraint("status IN ('APPROVED', 'FAILED')", name="ck_payments_status"),
        CheckConstraint(
            "(status = 'APPROVED' AND approved_at IS NOT NULL AND failed_at IS NULL) "
            "OR (status = 'FAILED' AND approved_at IS NULL AND failed_at IS NOT NULL)",
            name="ck_payments_terminal_timestamps",
        ),
        ForeignKeyConstraint(
            ["order_id"],
            ["known_orders.order_id"],
            name="fk_payments_order_id",
        ),
        UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_payments_user_idempotency_key",
        ),
        UniqueConstraint("order_id", name="uq_payments_order_id"),
        Index("ix_payments_order_id", "order_id"),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    order_id: Mapped[str] = mapped_column(String(64), nullable=False)
    user_id: Mapped[str] = mapped_column(String(64), nullable=False)
    amount: Mapped[int] = mapped_column(Integer, nullable=False)
    method: Mapped[str] = mapped_column(String(32), nullable=False)
    status: Mapped[str] = mapped_column(String(32), nullable=False)
    idempotency_key: Mapped[str] = mapped_column(String(128), nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), nullable=False
    )
    approved_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    failed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    failure_reason: Mapped[str | None] = mapped_column(String(128))


class ProcessedEventRecord(Base):
    __tablename__ = "processed_events"

    event_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    event_type: Mapped[str] = mapped_column(String(128), nullable=False)
    processed_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), nullable=False
    )


class OutboxEventRecord(Base):
    __tablename__ = "outbox_events"
    __table_args__ = (
        CheckConstraint(
            "attempts >= 0",
            name="ck_outbox_events_attempts_nonnegative",
        ),
        Index(
            "ix_outbox_events_pending",
            "published_at",
            "dead_lettered_at",
            "next_attempt_at",
        ),
    )

    event_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    event_type: Mapped[str] = mapped_column(String(128), nullable=False)
    aggregate_type: Mapped[str] = mapped_column(String(64), nullable=False)
    aggregate_id: Mapped[str] = mapped_column(String(64), nullable=False)
    topic: Mapped[str] = mapped_column(String(128), nullable=False)
    message_key: Mapped[str] = mapped_column(String(128), nullable=False)
    payload: Mapped[dict[str, str | int | bool | None]] = mapped_column(
        JSONB,
        nullable=False,
    )
    trace_context: Mapped[dict[str, JsonValue] | None] = mapped_column(
        JSONB,
        nullable=True,
    )
    occurred_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), nullable=False
    )
    attempts: Mapped[int] = mapped_column(
        Integer,
        default=0,
        server_default="0",
        nullable=False,
    )
    next_attempt_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    last_error: Mapped[str | None] = mapped_column(Text)
    published_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    dead_lettered_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
