from collections.abc import Sequence
from typing import override

import sqlalchemy as sa
from alembic import op
from alembic.util import CommandError
from sqlalchemy.engine import Connection

revision: str = "0001_notification_storage"
down_revision: str | None = None
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None

NOTIFICATION_TYPES = (
    "ORDER_CONFIRMED",
    "PAYMENT_FAILED",
    "ORDER_EXPIRED",
    "ORDER_CANCELED",
    "PAYMENT_REFUNDED",
    "REFUND_FAILED",
)


class DowngradeUnsupportedError(RuntimeError):
    def __init__(self, schema: str) -> None:
        super().__init__()
        self.schema = schema

    @override
    def __str__(self) -> str:
        return f"{self.schema} schema downgrade is not supported"


class DuplicateNotificationEventIdsError(CommandError):
    def __init__(self, event_ids: tuple[str, ...]) -> None:
        self.event_ids = event_ids
        joined_event_ids = ", ".join(event_ids)
        super().__init__(
            "notification migration blocked; "
            f"duplicate event_id values: {joined_event_ids}",
        )


def upgrade() -> None:
    connection = op.get_bind()
    table_names = set(sa.inspect(connection).get_table_names())
    if "notifications" not in table_names:
        _create_notifications()
    else:
        _reject_duplicate_event_ids(connection)
        _upgrade_notifications(connection)
    if "processed_events" not in table_names:
        _create_processed_events()
    _backfill_processed_events()


def downgrade() -> None:
    raise DowngradeUnsupportedError(schema="notification")


def _create_notifications() -> None:
    op.create_table(
        "notifications",
        sa.Column("id", sa.String(length=64), primary_key=True),
        sa.Column("event_id", sa.String(length=128), nullable=False),
        sa.Column("user_id", sa.String(length=64), nullable=False),
        sa.Column("order_id", sa.String(length=64), nullable=True),
        sa.Column(
            "type",
            sa.String(length=32),
            nullable=False,
            server_default="ORDER_CONFIRMED",
        ),
        sa.Column("title", sa.String(length=120), nullable=False),
        sa.Column("message", sa.String(length=500), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("read", sa.Boolean(), nullable=False),
        sa.CheckConstraint(
            sa.column("type").in_(NOTIFICATION_TYPES),
            name="ck_notifications_type",
        ),
        sa.UniqueConstraint("event_id", name="uq_notifications_event_id"),
    )
    op.create_index(
        "ix_notifications_user_created",
        "notifications",
        ["user_id", "created_at"],
    )


def _upgrade_notifications(connection: Connection) -> None:
    inspector = sa.inspect(connection)
    column_names = {column["name"] for column in inspector.get_columns("notifications")}
    if "type" not in column_names:
        op.add_column(
            "notifications",
            sa.Column(
                "type",
                sa.String(length=32),
                nullable=False,
                server_default="ORDER_CONFIRMED",
            ),
        )
    unique_constraints = inspector.get_unique_constraints("notifications")
    has_event_uniqueness = any(
        constraint.get("column_names") == ["event_id"]
        for constraint in unique_constraints
    )
    if not has_event_uniqueness:
        op.create_unique_constraint(
            "uq_notifications_event_id",
            "notifications",
            ["event_id"],
        )
    check_names = {
        constraint.get("name")
        for constraint in inspector.get_check_constraints("notifications")
    }
    if "ck_notifications_type" not in check_names:
        allowed_types = ", ".join(f"'{value}'" for value in NOTIFICATION_TYPES)
        op.create_check_constraint(
            "ck_notifications_type",
            "notifications",
            f"type IN ({allowed_types})",
        )
    index_names = {
        index.get("name") for index in inspector.get_indexes("notifications")
    }
    if "ix_notifications_user_created" not in index_names:
        op.create_index(
            "ix_notifications_user_created",
            "notifications",
            ["user_id", "created_at"],
        )


def _reject_duplicate_event_ids(connection: Connection) -> None:
    result = connection.execute(
        sa.text(
            "SELECT event_id FROM notifications "
            "GROUP BY event_id HAVING count(*) > 1 ORDER BY event_id"
        ),
    )
    event_ids = tuple(str(event_id) for event_id in result.scalars())
    if event_ids:
        raise DuplicateNotificationEventIdsError(event_ids)


def _create_processed_events() -> None:
    op.create_table(
        "processed_events",
        sa.Column("event_id", sa.String(length=128), primary_key=True),
        sa.Column("event_type", sa.String(length=128), nullable=False),
        sa.Column("processed_at", sa.DateTime(timezone=True), nullable=False),
    )


def _backfill_processed_events() -> None:
    op.execute(
        sa.text(
            """
            INSERT INTO processed_events (event_id, event_type, processed_at)
            SELECT event_id, 'notification.requested', created_at
            FROM notifications
            ON CONFLICT (event_id) DO NOTHING
            """,
        ),
    )
