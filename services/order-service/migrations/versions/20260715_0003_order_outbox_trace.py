from __future__ import annotations

from typing import Final

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from migrations.errors import UnsupportedDowngradeError

revision: Final = "20260715_0003"
down_revision: Final = "20260715_0002"
branch_labels: Final[str | None] = None
depends_on: Final[str | None] = None


def upgrade() -> None:
    columns = {
        column["name"]
        for column in sa.inspect(op.get_bind()).get_columns("outbox_events")
    }
    if "trace_context" in columns:
        return
    op.add_column(
        "outbox_events",
        sa.Column("trace_context", postgresql.JSONB(), nullable=True),
    )


def downgrade() -> None:
    raise UnsupportedDowngradeError(revision_id=revision)
