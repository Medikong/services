from __future__ import annotations

import sqlalchemy as sa
from sqlalchemy.engine.reflection import Inspector

from migrations.contract_specs import TARGET_TABLE_CONTRACTS, TableContract
from migrations.errors import LegacySchemaError


def validate_target_tables(
    inspector: Inspector,
    table_names: set[str],
    connection: sa.Connection,
) -> None:
    for table_name, contract in TARGET_TABLE_CONTRACTS.items():
        if table_name not in table_names:
            continue
        issues = _contract_issues(inspector, connection, table_name, contract)
        if issues:
            missing_columns = _missing_columns(inspector, table_name, contract)
            missing_detail = (
                f"; {table_name} is missing required columns: "
                + f"{', '.join(missing_columns)}"
                if missing_columns
                else ""
            )
            raise LegacySchemaError(
                detail=(
                    f"{table_name} contract mismatch: {'; '.join(issues)}"
                    + f"{missing_detail}; repair or remove the incompatible table "
                    + "before retrying"
                ),
            )
        incompatible_rows = connection.execute(contract.incompatible_rows).scalar_one()
        if incompatible_rows > 0:
            raise LegacySchemaError(
                detail=(
                    f"{table_name} contract mismatch: contains "
                    + f"{incompatible_rows} incompatible rows; repair the rows "
                    + "before retrying"
                ),
            )


def _contract_issues(
    inspector: Inspector,
    connection: sa.Connection,
    table_name: str,
    contract: TableContract,
) -> tuple[str, ...]:
    issues: list[str] = []
    actual_columns = inspector.get_columns(table_name)
    actual_names = tuple(column["name"] for column in actual_columns)
    expected_names = tuple(
        column.name
        for column in contract.columns
        if not column.optional or column.name in actual_names
    )
    if actual_names != expected_names:
        issues.append(f"columns expected {expected_names}, found {actual_names}")
    actual_by_name = {column["name"]: column for column in actual_columns}
    for column in contract.columns:
        actual = actual_by_name.get(column.name)
        if actual is None:
            continue
        actual_type = actual["type"].compile(dialect=connection.dialect)
        if actual_type != column.sql_type:
            issues.append(
                f"{column.name} type expected {column.sql_type}, found {actual_type}",
            )
        if actual["nullable"] != column.nullable:
            issues.append(
                f"{column.name} nullable expected {column.nullable}, "
                + f"found {actual['nullable']}",
            )
    primary_key = inspector.get_pk_constraint(table_name)
    actual_primary_key = tuple(primary_key["constrained_columns"])
    if actual_primary_key != contract.primary_key:
        issues.append(
            f"primary key expected {contract.primary_key}, found {actual_primary_key}",
        )
    if _unique_constraints(inspector, table_name) != frozenset(
        (constraint.name, constraint.columns)
        for constraint in contract.unique_constraints
    ):
        issues.append("unique constraints differ from the expected contract")
    if _foreign_keys(inspector, table_name) != frozenset(
        (key.columns, key.referred_table, key.referred_columns)
        for key in contract.foreign_keys
    ):
        issues.append("foreign keys differ from the expected contract")
    if _checks(inspector, table_name) != frozenset(
        (check.name, _normalize_sql(check.sqltext)) for check in contract.checks
    ):
        issues.append("check constraints differ from the expected contract")
    if _indexes(inspector, table_name) != frozenset(
        (index.name, index.columns, index.unique) for index in contract.indexes
    ):
        issues.append("indexes differ from the expected contract")
    return tuple(issues)


def _missing_columns(
    inspector: Inspector,
    table_name: str,
    contract: TableContract,
) -> tuple[str, ...]:
    actual_names = {column["name"] for column in inspector.get_columns(table_name)}
    return tuple(
        column.name
        for column in contract.columns
        if not column.optional and column.name not in actual_names
    )


def _unique_constraints(
    inspector: Inspector,
    table_name: str,
) -> frozenset[tuple[str | None, tuple[str, ...]]]:
    return frozenset(
        (constraint["name"], tuple(constraint["column_names"]))
        for constraint in inspector.get_unique_constraints(table_name)
    )


def _foreign_keys(
    inspector: Inspector,
    table_name: str,
) -> frozenset[tuple[tuple[str, ...], str, tuple[str, ...]]]:
    return frozenset(
        (
            tuple(key["constrained_columns"]),
            key["referred_table"],
            tuple(key["referred_columns"]),
        )
        for key in inspector.get_foreign_keys(table_name)
    )


def _checks(
    inspector: Inspector,
    table_name: str,
) -> frozenset[tuple[str | None, str]]:
    return frozenset(
        (constraint["name"], _normalize_sql(constraint["sqltext"]))
        for constraint in inspector.get_check_constraints(table_name)
    )


def _indexes(
    inspector: Inspector,
    table_name: str,
) -> frozenset[tuple[str | None, tuple[str | None, ...], bool]]:
    return frozenset(
        (index["name"], tuple(index["column_names"]), index["unique"])
        for index in inspector.get_indexes(table_name)
        if "duplicates_constraint" not in index
    )


def _normalize_sql(sqltext: str) -> str:
    return " ".join(sqltext.lower().split())
