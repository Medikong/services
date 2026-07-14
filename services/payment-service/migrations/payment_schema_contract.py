from dataclasses import dataclass
import sqlalchemy as sa


@dataclass(frozen=True, slots=True)
class ColumnSignature:
    name: str
    sql_type: str
    nullable: bool
    default: str | None = None
    identity: bool = False


@dataclass(frozen=True, slots=True)
class ConstraintSignature:
    name: str
    kind: str
    columns: str


@dataclass(frozen=True, slots=True)
class IndexSignature:
    name: str
    method: str
    unique: bool
    columns: str


@dataclass(frozen=True, slots=True)
class TableContract:
    name: str
    columns: tuple[ColumnSignature, ...]
    constraints: tuple[ConstraintSignature, ...]
    indexes: tuple[IndexSignature, ...] = ()


class LegacyPaymentSchemaError(RuntimeError):
    def __init__(self, issues: tuple[str, ...]) -> None:
        self.issues = issues
        super().__init__(
            "legacy payment schema is incompatible: " + "; ".join(issues),
        )


KNOWN_ORDERS_CONTRACT = TableContract(
    name="known_orders",
    columns=(
        ColumnSignature("order_id", "character varying(64)", False),
        ColumnSignature("user_id", "character varying(64)", False),
        ColumnSignature("amount", "integer", False),
        ColumnSignature("created_at", "timestamp with time zone", False),
    ),
    constraints=(ConstraintSignature("known_orders_pkey", "p", "order_id"),),
)

PAYMENTS_CONTRACT = TableContract(
    name="payments",
    columns=(
        ColumnSignature("id", "character varying(64)", False),
        ColumnSignature("order_id", "character varying(64)", False),
        ColumnSignature("user_id", "character varying(64)", False),
        ColumnSignature("amount", "integer", False),
        ColumnSignature("method", "character varying(32)", False),
        ColumnSignature("status", "character varying(32)", False),
        ColumnSignature("idempotency_key", "character varying(128)", False),
        ColumnSignature("created_at", "timestamp with time zone", False),
        ColumnSignature("approved_at", "timestamp with time zone", True),
        ColumnSignature("failed_at", "timestamp with time zone", True),
        ColumnSignature("failure_reason", "character varying(128)", True),
    ),
    constraints=(
        ConstraintSignature("payments_pkey", "p", "id"),
        ConstraintSignature(
            "uq_payments_user_idempotency_key",
            "u",
            "user_id,idempotency_key",
        ),
    ),
    indexes=(IndexSignature("ix_payments_order_id", "btree", False, "order_id"),),
)

LEGACY_PAYMENT_CONTRACTS = (KNOWN_ORDERS_CONTRACT, PAYMENTS_CONTRACT)


def validate_legacy_payment_schema(bind: sa.Connection) -> None:
    issues: list[str] = []
    for contract in LEGACY_PAYMENT_CONTRACTS:
        actual_columns = _columns(bind, contract.name)
        if actual_columns != contract.columns:
            issues.append(
                f"{contract.name} columns expected={contract.columns!r} "
                f"actual={actual_columns!r}",
            )
        actual_constraints = _constraints(bind, contract.name)
        if actual_constraints != contract.constraints:
            issues.append(
                f"{contract.name} constraints expected={contract.constraints!r} "
                f"actual={actual_constraints!r}",
            )
        actual_indexes = _indexes(bind, contract.name)
        if actual_indexes != contract.indexes:
            issues.append(
                f"{contract.name} indexes expected={contract.indexes!r} "
                f"actual={actual_indexes!r}",
            )
    if issues:
        raise LegacyPaymentSchemaError(tuple(issues))


def _columns(bind: sa.Connection, table_name: str) -> tuple[ColumnSignature, ...]:
    rows = bind.execute(
        sa.text(
            "SELECT a.attname, format_type(a.atttypid, a.atttypmod), "
            "NOT a.attnotnull, pg_get_expr(d.adbin, d.adrelid), a.attidentity <> '' "
            "FROM pg_attribute a "
            "JOIN pg_class t ON t.oid = a.attrelid "
            "JOIN pg_namespace n ON n.oid = t.relnamespace "
            "LEFT JOIN pg_attrdef d ON d.adrelid = t.oid AND d.adnum = a.attnum "
            "WHERE n.nspname = 'public' AND t.relname = :table_name "
            "AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum",
        ),
        {"table_name": table_name},
    ).tuples()
    return tuple(
        ColumnSignature(
            name=str(row[0]),
            sql_type=str(row[1]),
            nullable=bool(row[2]),
            default=None if row[3] is None else str(row[3]),
            identity=bool(row[4]),
        )
        for row in rows
    )


def _constraints(
    bind: sa.Connection,
    table_name: str,
) -> tuple[ConstraintSignature, ...]:
    rows = bind.execute(
        sa.text(
            "SELECT c.conname, c.contype::text, "
            "coalesce(string_agg(a.attname, ',' ORDER BY key.ordinality), '') "
            "FROM pg_constraint c "
            "JOIN pg_class t ON t.oid = c.conrelid "
            "JOIN pg_namespace n ON n.oid = t.relnamespace "
            "LEFT JOIN LATERAL unnest(c.conkey) WITH ORDINALITY "
            "AS key(attnum, ordinality) ON true "
            "LEFT JOIN pg_attribute a ON a.attrelid = t.oid "
            "AND a.attnum = key.attnum "
            "WHERE n.nspname = 'public' AND t.relname = :table_name "
            "GROUP BY c.conname, c.contype ORDER BY c.conname",
        ),
        {"table_name": table_name},
    ).tuples()
    return tuple(
        ConstraintSignature(str(row[0]), str(row[1]), str(row[2])) for row in rows
    )


def _indexes(bind: sa.Connection, table_name: str) -> tuple[IndexSignature, ...]:
    rows = bind.execute(
        sa.text(
            "SELECT index_table.relname, method.amname, index.indisunique, "
            "coalesce(string_agg(attribute.attname, ',' ORDER BY key.ordinality), '') "
            "FROM pg_index index "
            "JOIN pg_class table_rel ON table_rel.oid = index.indrelid "
            "JOIN pg_namespace n ON n.oid = table_rel.relnamespace "
            "JOIN pg_class index_table ON index_table.oid = index.indexrelid "
            "JOIN pg_am method ON method.oid = index_table.relam "
            "LEFT JOIN LATERAL unnest(index.indkey) WITH ORDINALITY "
            "AS key(attnum, ordinality) ON true "
            "LEFT JOIN pg_attribute attribute ON attribute.attrelid = table_rel.oid "
            "AND attribute.attnum = key.attnum "
            "WHERE n.nspname = 'public' AND table_rel.relname = :table_name "
            "AND NOT EXISTS (SELECT 1 FROM pg_constraint constraint_rel "
            "WHERE constraint_rel.conindid = index_table.oid) "
            "GROUP BY index_table.relname, method.amname, index.indisunique "
            "ORDER BY index_table.relname",
        ),
        {"table_name": table_name},
    ).tuples()
    return tuple(
        IndexSignature(str(row[0]), str(row[1]), bool(row[2]), str(row[3]))
        for row in rows
    )
