from datetime import datetime
from typing import Final

from sqlalchemy import (
    CheckConstraint,
    DateTime,
    ForeignKey,
    Integer,
    String,
    Text,
    UniqueConstraint,
)
from sqlalchemy.orm import Mapped, mapped_column

from app.records import Base


class RefundRecord(Base):
    __tablename__: Final = "refunds"
    __table_args__: tuple[CheckConstraint | UniqueConstraint, ...] = (
        CheckConstraint("amount >= 0", name="ck_refunds_amount_nonnegative"),
        CheckConstraint("attempts >= 0", name="ck_refunds_attempts_nonnegative"),
        CheckConstraint(
            "length(btrim(idempotency_fingerprint)) > 0",
            name="ck_refunds_idempotency_fingerprint_nonempty",
        ),
        CheckConstraint(
            "status IN ('REQUESTED', 'PROCESSING', 'COMPLETED', 'FAILED')",
            name="ck_refunds_status",
        ),
        UniqueConstraint("order_id", name="uq_refunds_order_id"),
        UniqueConstraint("payment_id", name="uq_refunds_payment_id"),
        UniqueConstraint(
            "idempotency_fingerprint",
            name="uq_refunds_idempotency_fingerprint",
        ),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    order_id: Mapped[str] = mapped_column(
        ForeignKey("known_orders.order_id", name="fk_refunds_order_id"),
    )
    payment_id: Mapped[str] = mapped_column(
        ForeignKey("payments.id", name="fk_refunds_payment_id"),
    )
    user_id: Mapped[str] = mapped_column(String(64))
    amount: Mapped[int] = mapped_column(Integer)
    status: Mapped[str] = mapped_column(String(32))
    reason: Mapped[str] = mapped_column(String(500))
    idempotency_fingerprint: Mapped[str] = mapped_column(String(128))
    attempts: Mapped[int] = mapped_column(Integer, default=0, server_default="0")
    last_error: Mapped[str | None] = mapped_column(Text, nullable=True)
    next_attempt_at: Mapped[datetime | None] = mapped_column(
        DateTime(timezone=True),
        nullable=True,
    )
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    completed_at: Mapped[datetime | None] = mapped_column(
        DateTime(timezone=True),
        nullable=True,
    )
