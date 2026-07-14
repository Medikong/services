"""Separate Catalog-owned metadata from the order inventory projection."""

from dataclasses import dataclass
from typing import Final

import sqlalchemy as sa
from alembic import op

revision: Final = "0002_inventory_projection"
down_revision: Final = "0001_catalog_projection"
branch_labels: Final[str | None] = None
depends_on: Final[str | None] = None


@dataclass(frozen=True, slots=True)
class UnsupportedDowngradeError(RuntimeError):
    """Raised when a destructive projection rollback is requested."""

    def __str__(self) -> str:
        """Return the operator-facing migration error."""
        return "catalog migrations do not support downgrade"


def upgrade() -> None:
    """Move projected values into a rebuildable inventory-only table."""
    op.create_table(
        "inventory_projections",
        sa.Column(
            "product_id",
            sa.String(length=64),
            sa.ForeignKey("products.id", ondelete="CASCADE"),
            primary_key=True,
        ),
        sa.Column(
            "drop_id",
            sa.String(length=64),
            sa.ForeignKey("drops.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("remaining_quantity", sa.Integer(), nullable=False),
        sa.Column("inventory_version", sa.BigInteger(), nullable=False),
        sa.CheckConstraint(
            "remaining_quantity >= 0",
            name="ck_inventory_projections_remaining_nonnegative",
        ),
        sa.CheckConstraint(
            "inventory_version >= 0",
            name="ck_inventory_projections_version_nonnegative",
        ),
    )
    op.create_index(
        "ix_inventory_projections_drop_id",
        "inventory_projections",
        ["drop_id"],
    )
    op.execute(
        sa.text(
            "INSERT INTO inventory_projections "
            "(product_id, drop_id, remaining_quantity, inventory_version) "
            "SELECT id, drop_id, remaining_quantity, inventory_version FROM products",
        ),
    )
    op.drop_column("products", "remaining_quantity")
    op.drop_column("products", "inventory_version")


def downgrade() -> None:
    """Reject destructive catalog migration rollback."""
    raise UnsupportedDowngradeError
