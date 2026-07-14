from __future__ import annotations

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql


def create_orders_table() -> None:
    _ = op.create_table(
        "orders",
        sa.Column("id", sa.String(64), primary_key=True),
        sa.Column("user_id", sa.String(64), nullable=False),
        sa.Column("drop_id", sa.String(64), nullable=False),
        sa.Column("product_id", sa.String(64), nullable=False),
        sa.Column("quantity", sa.Integer(), nullable=False),
        sa.Column("amount", sa.Integer(), nullable=False),
        sa.Column("status", sa.String(32), nullable=False),
        sa.Column("idempotency_key", sa.String(128), nullable=False),
        sa.Column("payment_id", sa.String(64), nullable=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("confirmed_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column(
            "fulfillment_status",
            sa.String(32),
            nullable=False,
            server_default="NOT_STARTED",
        ),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("cancel_pending_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("canceled_at", sa.DateTime(timezone=True), nullable=True),
        sa.UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_orders_user_idempotency_key",
        ),
        sa.CheckConstraint("quantity > 0", name="ck_orders_quantity_positive"),
        sa.CheckConstraint("amount >= 0", name="ck_orders_amount_nonnegative"),
        sa.CheckConstraint(
            "status IN ('PENDING_PAYMENT', 'CONFIRMED', 'PAYMENT_FAILED', "
            + "'CANCEL_PENDING', 'CANCELED', 'EXPIRED')",
            name="ck_orders_status",
        ),
        sa.CheckConstraint(
            "fulfillment_status IN ('NOT_STARTED', 'PREPARING', 'SHIPPED')",
            name="ck_orders_fulfillment_status",
        ),
    )
    op.create_index("ix_orders_user_status", "orders", ["user_id", "status"])
    op.create_index(
        "ix_orders_product_status",
        "orders",
        ["drop_id", "product_id", "status"],
    )


def create_processed_payment_events_table() -> None:
    _ = op.create_table(
        "processed_payment_events",
        sa.Column("event_id", sa.String(128), primary_key=True),
        sa.Column("event_type", sa.String(32), nullable=False),
        sa.Column("order_id", sa.String(64), nullable=False),
        sa.Column("payment_id", sa.String(64), nullable=False),
        sa.Column("processed_at", sa.DateTime(timezone=True), nullable=False),
    )


def create_cancellation_requests_table() -> None:
    _ = op.create_table(
        "cancellation_requests",
        sa.Column("id", sa.String(64), primary_key=True),
        sa.Column(
            "order_id",
            sa.String(64),
            sa.ForeignKey("orders.id"),
            nullable=False,
        ),
        sa.Column("user_id", sa.String(64), nullable=False),
        sa.Column("idempotency_key", sa.String(128), nullable=False),
        sa.Column("reason", sa.String(500), nullable=False),
        sa.Column(
            "refund_status",
            sa.String(32),
            nullable=False,
            server_default="REQUESTED",
        ),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("updated_at", sa.DateTime(timezone=True), nullable=False),
        sa.UniqueConstraint("order_id", name="uq_cancellation_requests_order_id"),
        sa.UniqueConstraint(
            "user_id",
            "idempotency_key",
            name="uq_cancellation_requests_user_idempotency_key",
        ),
        sa.CheckConstraint(
            "refund_status IN ('REQUESTED', 'PROCESSING', 'COMPLETED', 'FAILED')",
            name="ck_cancellation_requests_refund_status",
        ),
    )


def create_inventory_items_table() -> None:
    _ = op.create_table(
        "inventory_items",
        sa.Column("drop_id", sa.String(64), primary_key=True),
        sa.Column("product_id", sa.String(64), primary_key=True),
        sa.Column("total_quantity", sa.Integer(), nullable=False),
        sa.Column("reserved_quantity", sa.Integer(), nullable=False),
        sa.Column("sold_quantity", sa.Integer(), nullable=False),
        sa.Column("version", sa.BigInteger(), nullable=False, server_default="0"),
        sa.CheckConstraint(
            "total_quantity >= 0",
            name="ck_inventory_items_total_nonnegative",
        ),
        sa.CheckConstraint(
            "reserved_quantity >= 0",
            name="ck_inventory_items_reserved_nonnegative",
        ),
        sa.CheckConstraint(
            "sold_quantity >= 0",
            name="ck_inventory_items_sold_nonnegative",
        ),
        sa.CheckConstraint(
            "reserved_quantity + sold_quantity <= total_quantity",
            name="ck_inventory_items_consistent",
        ),
        sa.CheckConstraint(
            "version >= 0", name="ck_inventory_items_version_nonnegative"
        ),
    )


def create_processed_events_table() -> None:
    _ = op.create_table(
        "processed_events",
        sa.Column("event_id", sa.String(128), primary_key=True),
        sa.Column("event_type", sa.String(128), nullable=False),
        sa.Column("aggregate_type", sa.String(64), nullable=False),
        sa.Column("aggregate_id", sa.String(64), nullable=False),
        sa.Column("processed_at", sa.DateTime(timezone=True), nullable=False),
    )


def create_outbox_events_table() -> None:
    _ = op.create_table(
        "outbox_events",
        sa.Column("event_id", sa.String(128), primary_key=True),
        sa.Column("event_type", sa.String(128), nullable=False),
        sa.Column("aggregate_type", sa.String(64), nullable=False),
        sa.Column("aggregate_id", sa.String(64), nullable=False),
        sa.Column("topic", sa.String(128), nullable=False),
        sa.Column("message_key", sa.String(128), nullable=False),
        sa.Column("payload", postgresql.JSONB(), nullable=False),
        sa.Column("occurred_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("attempts", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("next_attempt_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("last_error", sa.Text(), nullable=True),
        sa.Column("published_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("dead_lettered_at", sa.DateTime(timezone=True), nullable=True),
        sa.CheckConstraint(
            "attempts >= 0", name="ck_outbox_events_attempts_nonnegative"
        ),
    )
    op.create_index(
        "ix_outbox_events_pending",
        "outbox_events",
        ["published_at", "dead_lettered_at", "next_attempt_at"],
    )
