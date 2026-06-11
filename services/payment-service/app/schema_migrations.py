from sqlalchemy import Column, JSON, Engine, inspect, text
from sqlalchemy.schema import CreateColumn


def run_schema_migrations(engine: Engine) -> None:
    """Apply temporary additive schema fixes that create_all() cannot handle."""
    # TEMPORARY: this is a short-lived bridge until payment-service adopts a
    # real migration flow. Keep only safe, additive fixes here and remove this
    # module once the schema is managed by a proper migration step.
    with engine.begin() as connection:
        _add_column_if_missing(
            connection,
            table_name="payment_events",
            column=Column("trace_context", JSON, nullable=True),
        )


def _add_column_if_missing(connection, *, table_name: str, column: Column) -> None:
    inspector = inspect(connection)
    if not inspector.has_table(table_name):
        return

    column_names = {existing_column["name"] for existing_column in inspector.get_columns(table_name)}
    if column.name in column_names:
        return

    preparer = connection.dialect.identifier_preparer
    table_sql = preparer.quote(table_name)
    column_sql = str(CreateColumn(column).compile(dialect=connection.dialect))
    connection.execute(text(f"ALTER TABLE {table_sql} ADD COLUMN {column_sql}"))
