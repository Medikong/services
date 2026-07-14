from __future__ import annotations

from typing import Final

from alembic import op
import sqlalchemy as sa
from sqlalchemy import inspect

from migrations.contracts import validate_target_tables
from migrations.errors import LegacySchemaError
from migrations.legacy import upgrade_current_orders
from migrations.tables import (
    create_cancellation_requests_table,
    create_inventory_items_table,
    create_orders_table,
    create_outbox_events_table,
    create_processed_events_table,
    create_processed_payment_events_table,
)

KNOWN_INVENTORY: Final = (
    ("drop-001", "product-001", 42),
    ("drop-sold-out-001", "product-sold-out-001", 42),
)


def upgrade_schema() -> None:
    connection = op.get_bind()
    inspector = inspect(connection)
    table_names = set(inspector.get_table_names())
    current_tables = table_names & {"orders", "processed_payment_events"}
    if len(current_tables) == 1:
        raise LegacySchemaError(
            detail="orders and processed_payment_events must both exist or both be absent",
        )

    validate_target_tables(inspector, table_names, connection)

    if "orders" not in table_names:
        create_orders_table()
        create_processed_payment_events_table()
    else:
        upgrade_current_orders(inspector)

    if "cancellation_requests" not in table_names:
        create_cancellation_requests_table()
    if "inventory_items" not in table_names:
        create_inventory_items_table()
    if "processed_events" not in table_names:
        create_processed_events_table()
    if "outbox_events" not in table_names:
        create_outbox_events_table()

    _seed_inventory(connection)


def _seed_inventory(connection: sa.Connection) -> None:
    known_inventory_insert = sa.text(
        """
        INSERT INTO inventory_items (
            drop_id, product_id, total_quantity,
            reserved_quantity, sold_quantity, version
        )
        SELECT :drop_id, :product_id, :total_quantity,
            COALESCE(SUM(quantity) FILTER (
                WHERE status = 'PENDING_PAYMENT'
            ), 0),
            COALESCE(SUM(quantity) FILTER (
                WHERE status IN ('CONFIRMED', 'CANCEL_PENDING')
            ), 0),
            0
        FROM orders
        WHERE drop_id = :drop_id AND product_id = :product_id
        ON CONFLICT (drop_id, product_id) DO NOTHING
        """,
    ).bindparams(
        sa.bindparam("drop_id", type_=sa.String(64)),
        sa.bindparam("product_id", type_=sa.String(64)),
        sa.bindparam("total_quantity", type_=sa.Integer()),
    )
    for drop_id, product_id, total_quantity in KNOWN_INVENTORY:
        _ = connection.execute(
            known_inventory_insert,
            {
                "drop_id": drop_id,
                "product_id": product_id,
                "total_quantity": total_quantity,
            },
        )

    _ = connection.execute(
        sa.text(
            """
            INSERT INTO inventory_items (
                drop_id, product_id, total_quantity,
                reserved_quantity, sold_quantity, version
            )
            SELECT drop_id, product_id,
                COALESCE(SUM(quantity) FILTER (
                    WHERE status IN ('PENDING_PAYMENT', 'CONFIRMED', 'CANCEL_PENDING')
                ), 0),
                COALESCE(SUM(quantity) FILTER (
                    WHERE status = 'PENDING_PAYMENT'
                ), 0),
                COALESCE(SUM(quantity) FILTER (
                    WHERE status IN ('CONFIRMED', 'CANCEL_PENDING')
                ), 0),
                0
            FROM orders
            WHERE (drop_id, product_id) NOT IN (
                ('drop-001', 'product-001'),
                ('drop-sold-out-001', 'product-sold-out-001')
            )
            GROUP BY drop_id, product_id
            ON CONFLICT (drop_id, product_id) DO NOTHING
            """,
        ),
    )
