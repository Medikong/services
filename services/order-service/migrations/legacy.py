from __future__ import annotations

from typing import Final

from alembic import op
import sqlalchemy as sa
from sqlalchemy.engine.reflection import Inspector

from migrations.errors import LegacyInventoryContradictionError, LegacySchemaError

CURRENT_ORDER_COLUMNS: Final = frozenset(
    {
        "id",
        "user_id",
        "drop_id",
        "product_id",
        "quantity",
        "amount",
        "status",
        "idempotency_key",
        "payment_id",
        "created_at",
        "confirmed_at",
    },
)
KNOWN_PRODUCTS: Final = (
    ("drop-001", "product-001"),
    ("drop-sold-out-001", "product-sold-out-001"),
)
ORDER_DROP_ID = sa.column("drop_id", sa.String(64))
ORDER_PRODUCT_ID = sa.column("product_id", sa.String(64))
ORDER_QUANTITY = sa.column("quantity", sa.Integer())
ORDER_AMOUNT = sa.column("amount", sa.Integer())
ORDER_STATUS = sa.column("status", sa.String(32))
ORDERS = sa.table(
    "orders",
    ORDER_DROP_ID,
    ORDER_PRODUCT_ID,
    ORDER_QUANTITY,
    ORDER_AMOUNT,
    ORDER_STATUS,
)


def upgrade_current_orders(inspector: Inspector) -> None:
    present_columns = {column["name"] for column in inspector.get_columns("orders")}
    missing_columns = CURRENT_ORDER_COLUMNS - present_columns
    if missing_columns:
        raise LegacySchemaError(
            detail=f"orders is missing required columns: {', '.join(sorted(missing_columns))}",
        )

    _validate_current_orders()
    lifecycle_columns = (
        sa.Column(
            "fulfillment_status",
            sa.String(32),
            nullable=False,
            server_default="NOT_STARTED",
        ),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("cancel_pending_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("canceled_at", sa.DateTime(timezone=True), nullable=True),
    )
    for column in lifecycle_columns:
        if column.name not in present_columns:
            op.add_column("orders", column)

    constraint_names = {
        constraint["name"] for constraint in inspector.get_check_constraints("orders")
    }
    checks = (
        ("ck_orders_quantity_positive", "quantity > 0"),
        ("ck_orders_amount_nonnegative", "amount >= 0"),
        (
            "ck_orders_status",
            "status IN ('PENDING_PAYMENT', 'CONFIRMED', 'PAYMENT_FAILED', "
            + "'CANCEL_PENDING', 'CANCELED', 'EXPIRED')",
        ),
        (
            "ck_orders_fulfillment_status",
            "fulfillment_status IN ('NOT_STARTED', 'PREPARING', 'SHIPPED')",
        ),
    )
    for constraint_name, condition in checks:
        if constraint_name not in constraint_names:
            op.create_check_constraint(constraint_name, "orders", condition)


def _validate_current_orders() -> None:
    connection = op.get_bind()
    active_statuses = (
        "PENDING_PAYMENT",
        "CONFIRMED",
        "PAYMENT_FAILED",
        "CANCEL_PENDING",
        "CANCELED",
        "EXPIRED",
    )
    invalid_order_count = connection.execute(
        sa.select(sa.func.count())
        .select_from(ORDERS)
        .where(
            sa.or_(
                ORDER_QUANTITY <= 0,
                ORDER_AMOUNT < 0,
                ORDER_STATUS.not_in(active_statuses),
            ),
        ),
    ).scalar_one()
    if invalid_order_count > 0:
        raise LegacySchemaError(
            detail=f"orders contains {invalid_order_count} invalid lifecycle rows",
        )

    reserved_quantity = sa.cast(
        sa.func.coalesce(
            sa.func.sum(ORDER_QUANTITY).filter(ORDER_STATUS == "PENDING_PAYMENT"),
            0,
        ),
        sa.Integer(),
    )
    sold_quantity = sa.cast(
        sa.func.coalesce(
            sa.func.sum(ORDER_QUANTITY).filter(
                ORDER_STATUS.in_(("CONFIRMED", "CANCEL_PENDING")),
            ),
            0,
        ),
        sa.Integer(),
    )
    contradiction = (
        connection.execute(
            sa.select(
                ORDER_DROP_ID,
                ORDER_PRODUCT_ID,
                reserved_quantity,
                sold_quantity,
            )
            .select_from(ORDERS)
            .where(sa.tuple_(ORDER_DROP_ID, ORDER_PRODUCT_ID).in_(KNOWN_PRODUCTS))
            .group_by(ORDER_DROP_ID, ORDER_PRODUCT_ID)
            .having(reserved_quantity + sold_quantity > 42)
            .limit(1),
        )
        .tuples()
        .first()
    )
    if contradiction is not None:
        raise LegacyInventoryContradictionError(
            drop_id=contradiction[0],
            product_id=contradiction[1],
            reserved_quantity=contradiction[2],
            sold_quantity=contradiction[3],
        )
