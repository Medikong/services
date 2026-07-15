from __future__ import annotations

from typing import Final

from alembic import op
import sqlalchemy as sa

from migrations.errors import ExpiryIndexDefinitionError, UnsupportedDowngradeError

revision: Final = "20260715_0002"
down_revision: Final = "20260714_0001"
branch_labels: Final[str | None] = None
depends_on: Final[str | None] = None


def upgrade() -> None:
    op.execute(
        sa.text(
            "UPDATE orders "
            "SET expires_at = created_at + INTERVAL '300 seconds' "
            "WHERE status = 'PENDING_PAYMENT' AND expires_at IS NULL"
        )
    )
    existing = _expiry_index_signature()
    if existing is None:
        op.create_index(
            "ix_orders_pending_expiry",
            "orders",
            ["expires_at", "id"],
            postgresql_where=sa.text(
                "status = 'PENDING_PAYMENT' AND expires_at IS NOT NULL"
            ),
        )
    elif not _is_expected_expiry_index(existing):
        raise ExpiryIndexDefinitionError(actual="/".join(map(str, existing)))


def downgrade() -> None:
    raise UnsupportedDowngradeError(revision_id=revision)


def _expiry_index_signature() -> tuple[str, bool, bool, str, str] | None:
    row = (
        op.get_bind()
        .execute(
            sa.text(
                "SELECT method.amname, index.indisunique, index.indisvalid, "
                "string_agg(attribute.attname, ',' ORDER BY key.ordinality), "
                "pg_get_expr(index.indpred, index.indrelid) "
                "FROM pg_index index "
                "JOIN pg_class table_rel ON table_rel.oid=index.indrelid "
                "JOIN pg_namespace namespace ON namespace.oid=table_rel.relnamespace "
                "JOIN pg_class index_rel ON index_rel.oid=index.indexrelid "
                "JOIN pg_am method ON method.oid=index_rel.relam "
                "JOIN LATERAL unnest(index.indkey) WITH ORDINALITY "
                "AS key(attnum, ordinality) ON true "
                "JOIN pg_attribute attribute ON attribute.attrelid=table_rel.oid "
                "AND attribute.attnum=key.attnum "
                "WHERE namespace.nspname=current_schema() "
                "AND table_rel.relname='orders' "
                "AND index_rel.relname='ix_orders_pending_expiry' "
                "GROUP BY method.amname, index.indisunique, index.indisvalid, "
                "index.indpred, index.indrelid"
            )
        )
        .tuples()
        .one_or_none()
    )
    if row is None:
        return None
    return str(row[0]), bool(row[1]), bool(row[2]), str(row[3]), str(row[4])


def _is_expected_expiry_index(
    signature: tuple[str, bool, bool, str, str],
) -> bool:
    method, unique, valid, columns, predicate = signature
    canonical_predicate = (
        predicate.lower()
        .replace("::text", "")
        .replace(" ", "")
        .replace("(", "")
        .replace(")", "")
    )
    return (
        method == "btree"
        and not unique
        and valid
        and columns == "expires_at,id"
        and canonical_predicate == "status='pending_payment'andexpires_atisnotnull"
    )
