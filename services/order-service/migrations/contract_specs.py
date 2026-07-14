from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from types import MappingProxyType
from typing import Final

import sqlalchemy as sa


def _incompatible_rows(
    table_name: str,
    condition: str,
) -> sa.Select[tuple[int]]:
    return (
        sa.select(sa.func.count())
        .select_from(sa.table(table_name))
        .where(sa.literal_column(condition, type_=sa.Boolean()))
    )


@dataclass(frozen=True, slots=True)
class ColumnSpec:
    name: str
    sql_type: str
    nullable: bool = False


@dataclass(frozen=True, slots=True)
class NamedColumns:
    name: str
    columns: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class ForeignKeySpec:
    columns: tuple[str, ...]
    referred_table: str
    referred_columns: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class CheckSpec:
    name: str
    sqltext: str


@dataclass(frozen=True, slots=True)
class IndexSpec:
    name: str
    columns: tuple[str, ...]
    unique: bool = False


@dataclass(frozen=True, slots=True)
class TableContract:
    columns: tuple[ColumnSpec, ...]
    primary_key: tuple[str, ...]
    incompatible_rows: sa.Select[tuple[int]]
    unique_constraints: tuple[NamedColumns, ...] = ()
    foreign_keys: tuple[ForeignKeySpec, ...] = ()
    checks: tuple[CheckSpec, ...] = ()
    indexes: tuple[IndexSpec, ...] = ()


TARGET_TABLE_CONTRACTS: Final[Mapping[str, TableContract]] = MappingProxyType(
    {
        "processed_payment_events": TableContract(
            columns=(
                ColumnSpec("event_id", "VARCHAR(128)"),
                ColumnSpec("event_type", "VARCHAR(32)"),
                ColumnSpec("order_id", "VARCHAR(64)"),
                ColumnSpec("payment_id", "VARCHAR(64)"),
                ColumnSpec("processed_at", "TIMESTAMP WITH TIME ZONE"),
            ),
            primary_key=("event_id",),
            incompatible_rows=_incompatible_rows(
                "processed_payment_events",
                "event_id IS NULL OR event_type IS NULL OR order_id IS NULL "
                + "OR payment_id IS NULL OR processed_at IS NULL",
            ),
        ),
        "cancellation_requests": TableContract(
            columns=(
                ColumnSpec("id", "VARCHAR(64)"),
                ColumnSpec("order_id", "VARCHAR(64)"),
                ColumnSpec("user_id", "VARCHAR(64)"),
                ColumnSpec("idempotency_key", "VARCHAR(128)"),
                ColumnSpec("reason", "VARCHAR(500)"),
                ColumnSpec("refund_status", "VARCHAR(32)"),
                ColumnSpec("created_at", "TIMESTAMP WITH TIME ZONE"),
                ColumnSpec("updated_at", "TIMESTAMP WITH TIME ZONE"),
            ),
            primary_key=("id",),
            unique_constraints=(
                NamedColumns("uq_cancellation_requests_order_id", ("order_id",)),
                NamedColumns(
                    "uq_cancellation_requests_user_idempotency_key",
                    ("user_id", "idempotency_key"),
                ),
            ),
            foreign_keys=(ForeignKeySpec(("order_id",), "orders", ("id",)),),
            checks=(
                CheckSpec(
                    "ck_cancellation_requests_refund_status",
                    "refund_status::text = any (array['requested'::character "
                    + "varying, 'processing'::character varying, "
                    + "'completed'::character varying, 'failed'::character "
                    + "varying]::text[])",
                ),
            ),
            incompatible_rows=_incompatible_rows(
                "cancellation_requests",
                "refund_status NOT IN "
                + "('REQUESTED', 'PROCESSING', 'COMPLETED', 'FAILED') "
                + "OR NOT EXISTS (SELECT 1 FROM orders "
                + "WHERE orders.id = cancellation_requests.order_id)",
            ),
        ),
        "inventory_items": TableContract(
            columns=(
                ColumnSpec("drop_id", "VARCHAR(64)"),
                ColumnSpec("product_id", "VARCHAR(64)"),
                ColumnSpec("total_quantity", "INTEGER"),
                ColumnSpec("reserved_quantity", "INTEGER"),
                ColumnSpec("sold_quantity", "INTEGER"),
                ColumnSpec("version", "BIGINT"),
            ),
            primary_key=("drop_id", "product_id"),
            checks=(
                CheckSpec(
                    "ck_inventory_items_consistent",
                    "(reserved_quantity + sold_quantity) <= total_quantity",
                ),
                CheckSpec(
                    "ck_inventory_items_reserved_nonnegative",
                    "reserved_quantity >= 0",
                ),
                CheckSpec(
                    "ck_inventory_items_sold_nonnegative",
                    "sold_quantity >= 0",
                ),
                CheckSpec(
                    "ck_inventory_items_total_nonnegative",
                    "total_quantity >= 0",
                ),
                CheckSpec("ck_inventory_items_version_nonnegative", "version >= 0"),
            ),
            incompatible_rows=_incompatible_rows(
                "inventory_items",
                "total_quantity < 0 OR reserved_quantity < 0 "
                + "OR sold_quantity < 0 OR version < 0 "
                + "OR reserved_quantity + sold_quantity > total_quantity",
            ),
        ),
        "processed_events": TableContract(
            columns=(
                ColumnSpec("event_id", "VARCHAR(128)"),
                ColumnSpec("event_type", "VARCHAR(128)"),
                ColumnSpec("aggregate_type", "VARCHAR(64)"),
                ColumnSpec("aggregate_id", "VARCHAR(64)"),
                ColumnSpec("processed_at", "TIMESTAMP WITH TIME ZONE"),
            ),
            primary_key=("event_id",),
            incompatible_rows=_incompatible_rows(
                "processed_events",
                "event_id IS NULL OR event_type IS NULL "
                + "OR aggregate_type IS NULL OR aggregate_id IS NULL "
                + "OR processed_at IS NULL",
            ),
        ),
        "outbox_events": TableContract(
            columns=(
                ColumnSpec("event_id", "VARCHAR(128)"),
                ColumnSpec("event_type", "VARCHAR(128)"),
                ColumnSpec("aggregate_type", "VARCHAR(64)"),
                ColumnSpec("aggregate_id", "VARCHAR(64)"),
                ColumnSpec("topic", "VARCHAR(128)"),
                ColumnSpec("message_key", "VARCHAR(128)"),
                ColumnSpec("payload", "JSONB"),
                ColumnSpec("occurred_at", "TIMESTAMP WITH TIME ZONE"),
                ColumnSpec("attempts", "INTEGER"),
                ColumnSpec("next_attempt_at", "TIMESTAMP WITH TIME ZONE", True),
                ColumnSpec("last_error", "TEXT", True),
                ColumnSpec("published_at", "TIMESTAMP WITH TIME ZONE", True),
                ColumnSpec("dead_lettered_at", "TIMESTAMP WITH TIME ZONE", True),
            ),
            primary_key=("event_id",),
            checks=(
                CheckSpec("ck_outbox_events_attempts_nonnegative", "attempts >= 0"),
            ),
            indexes=(
                IndexSpec(
                    "ix_outbox_events_pending",
                    ("published_at", "dead_lettered_at", "next_attempt_at"),
                ),
            ),
            incompatible_rows=_incompatible_rows("outbox_events", "attempts < 0"),
        ),
    },
)
