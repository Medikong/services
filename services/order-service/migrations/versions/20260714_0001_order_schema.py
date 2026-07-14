from __future__ import annotations

from typing import Final

from migrations.errors import UnsupportedDowngradeError
from migrations.schema import upgrade_schema

revision: Final = "20260714_0001"
down_revision: Final[str | None] = None
branch_labels: Final[str | None] = None
depends_on: Final[str | None] = None


def upgrade() -> None:
    upgrade_schema()


def downgrade() -> None:
    raise UnsupportedDowngradeError(revision_id=revision)
