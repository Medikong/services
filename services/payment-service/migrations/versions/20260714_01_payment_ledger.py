from collections.abc import Sequence

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

from migrations.payment_schema_contract import (
    LegacyPaymentSchemaError,
    validate_legacy_payment_schema,
)
from migrations.payment_legacy_upgrade import (
    apply_legacy_target_constraints,
    raise_for_invalid_legacy_rows,
)

revision: str = "20260714_01"
down_revision: str | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    bind = op.get_bind()
    inspector = sa.inspect(bind)
    payment_table_exists = inspector.has_table("payments")
    known_order_table_exists = inspector.has_table("known_orders")

    if payment_table_exists != known_order_table_exists:
        missing_table = "known_orders" if payment_table_exists else "payments"
        raise LegacyPaymentSchemaError((f"missing required table {missing_table}",))

    if payment_table_exists and known_order_table_exists:
        validate_legacy_payment_schema(bind)
        _raise_for_duplicate_order_payments(bind)
        raise_for_invalid_legacy_rows(bind)
        apply_legacy_target_constraints()
    else:
        _create_known_orders()
        _create_payments()

    _create_refunds()
    _create_processed_events()
    _create_outbox_events()


def downgrade() -> None:
    raise NotImplementedError(
        "payment-service migrations do not support downgrade",
    )


def _raise_for_duplicate_order_payments(bind: sa.Connection) -> None:
    duplicates = (
        bind.execute(
            sa.text(
                "".join(
                    [
                        "SELECT order_id, ",
                        "string_agg(id || ':' || status, ', ' ORDER BY id) AS records ",
                        "FROM payments GROUP BY order_id HAVING count(*) > 1 ",
                        "ORDER BY order_id",
                    ],
                ),
            ),
        )
        .mappings()
        .all()
    )
    if not duplicates:
        return
    details = "; ".join(
        f"order_id={row['order_id']} records=[{row['records']}]" for row in duplicates
    )
    message = " ".join(
        [
            f"cannot enforce one terminal payment per order: {details};",
            "remove or reconcile the conflicting payment records and rerun",
            "`alembic upgrade head`",
        ],
    )
    raise RuntimeError(message)


def _create_known_orders() -> None:
    _ = op.create_table(
        "known_orders",
        sa.Column("order_id", sa.String(length=64), primary_key=True),
        sa.Column("user_id", sa.String(length=64), nullable=False),
        sa.Column("amount", sa.Integer(), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.CheckConstraint(
            "amount >= 0",
            name="ck_known_orders_amount_nonnegative",
        ),
    )


def _create_payments() -> None:
    _ = op.create_table(
        "payments",
        sa.Column("id", sa.String(length=64), primary_key=True),
        sa.Column("order_id", sa.String(length=64), nullable=False),
        sa.Column("user_id", sa.String(length=64), nullable=False),
        sa.Column("amount", sa.Integer(), nullable=False),
        sa.Column("method", sa.String(length=32), nullable=False),
        sa.Column("status", sa.String(length=32), nullable=False),
        sa.Column("idempotency_key", sa.String(length=128), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("approved_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("failed_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("failure_reason", sa.String(length=128), nullable=True),
        sa.CheckConstraint("amount >= 0", name="ck_payments_amount_nonnegative"),
        sa.CheckConstraint("method = 'MOCK_CARD'", name="ck_payments_method"),
        sa.CheckConstraint(
            "status IN ('APPROVED', 'FAILED')",
            name="ck_payments_status",
        ),
        sa.CheckConstraint(
            "(status = 'APPROVED' AND approved_at IS NOT NULL AND failed_at IS NULL) "
            "OR (status = 'FAILED' AND approved_at IS NULL AND failed_at IS NOT NULL)",
            name="ck_payments_terminal_timestamps",
        ),
        sa.ForeignKeyConstraint(
            ["order_id"],
            ["known_orders.order_id"],
            name="fk_payments_order_id",
        ),
        sa.UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_payments_user_idempotency_key",
        ),
        sa.UniqueConstraint("order_id", name="uq_payments_order_id"),
    )
    op.create_index("ix_payments_order_id", "payments", ["order_id"])


def _create_refunds() -> None:
    _ = op.create_table(
        "refunds",
        sa.Column("id", sa.String(length=64), primary_key=True),
        sa.Column("order_id", sa.String(length=64), nullable=False),
        sa.Column("payment_id", sa.String(length=64), nullable=False),
        sa.Column("user_id", sa.String(length=64), nullable=False),
        sa.Column("amount", sa.Integer(), nullable=False),
        sa.Column("status", sa.String(length=32), nullable=False),
        sa.Column("reason", sa.String(length=500), nullable=False),
        sa.Column("idempotency_fingerprint", sa.String(length=128), nullable=False),
        sa.Column("attempts", sa.Integer(), server_default="0", nullable=False),
        sa.Column("last_error", sa.Text(), nullable=True),
        sa.Column("next_attempt_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("completed_at", sa.DateTime(timezone=True), nullable=True),
        sa.CheckConstraint("amount >= 0", name="ck_refunds_amount_nonnegative"),
        sa.CheckConstraint("attempts >= 0", name="ck_refunds_attempts_nonnegative"),
        sa.CheckConstraint(
            "length(btrim(idempotency_fingerprint)) > 0",
            name="ck_refunds_idempotency_fingerprint_nonempty",
        ),
        sa.CheckConstraint(
            "status IN ('REQUESTED', 'PROCESSING', 'COMPLETED', 'FAILED')",
            name="ck_refunds_status",
        ),
        sa.ForeignKeyConstraint(
            ["order_id"],
            ["known_orders.order_id"],
            name="fk_refunds_order_id",
        ),
        sa.ForeignKeyConstraint(
            ["payment_id"],
            ["payments.id"],
            name="fk_refunds_payment_id",
        ),
        sa.UniqueConstraint("order_id", name="uq_refunds_order_id"),
        sa.UniqueConstraint("payment_id", name="uq_refunds_payment_id"),
        sa.UniqueConstraint(
            "idempotency_fingerprint",
            name="uq_refunds_idempotency_fingerprint",
        ),
    )


def _create_processed_events() -> None:
    _ = op.create_table(
        "processed_events",
        sa.Column("event_id", sa.String(length=128), primary_key=True),
        sa.Column("event_type", sa.String(length=128), nullable=False),
        sa.Column("processed_at", sa.DateTime(timezone=True), nullable=False),
    )


def _create_outbox_events() -> None:
    _ = op.create_table(
        "outbox_events",
        sa.Column("event_id", sa.String(length=128), primary_key=True),
        sa.Column("event_type", sa.String(length=128), nullable=False),
        sa.Column("aggregate_type", sa.String(length=64), nullable=False),
        sa.Column("aggregate_id", sa.String(length=64), nullable=False),
        sa.Column("topic", sa.String(length=128), nullable=False),
        sa.Column("message_key", sa.String(length=128), nullable=False),
        sa.Column("payload", postgresql.JSONB(astext_type=sa.Text()), nullable=False),
        sa.Column("occurred_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("attempts", sa.Integer(), server_default="0", nullable=False),
        sa.Column("next_attempt_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("last_error", sa.Text(), nullable=True),
        sa.Column("published_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("dead_lettered_at", sa.DateTime(timezone=True), nullable=True),
        sa.CheckConstraint(
            "attempts >= 0",
            name="ck_outbox_events_attempts_nonnegative",
        ),
    )
    op.create_index(
        "ix_outbox_events_pending",
        "outbox_events",
        ["published_at", "dead_lettered_at", "next_attempt_at"],
    )
