"""Create and seed catalog metadata with inventory projection fields."""

from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Final

import sqlalchemy as sa
from alembic import op

revision: Final = "0001_catalog_projection"
down_revision: Final[str | None] = None
branch_labels: Final[str | None] = None
depends_on: Final[str | None] = None


@dataclass(frozen=True, slots=True)
class UnsupportedDowngradeError(RuntimeError):
    """Raised when a destructive catalog schema rollback is requested."""

    def __str__(self) -> str:
        """Return the operator-facing migration error."""
        return "catalog migrations do not support downgrade"


def upgrade() -> None:
    """Create catalog metadata and projection tables with deterministic seeds."""
    drops = op.create_table(
        "drops",
        sa.Column("id", sa.String(length=64), primary_key=True),
        sa.Column("title", sa.String(length=255), nullable=False),
        sa.Column("status", sa.String(length=16), nullable=False),
        sa.Column("opens_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("closes_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("description", sa.Text(), nullable=False),
        sa.CheckConstraint(
            "status IN ('UPCOMING', 'OPEN', 'SOLD_OUT', 'CLOSED')",
            name="ck_drops_status",
        ),
    )
    products = op.create_table(
        "products",
        sa.Column("id", sa.String(length=64), primary_key=True),
        sa.Column(
            "drop_id",
            sa.String(length=64),
            sa.ForeignKey("drops.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("name", sa.String(length=255), nullable=False),
        sa.Column("price", sa.Integer(), nullable=False),
        sa.Column(
            "remaining_quantity",
            sa.Integer(),
            nullable=False,
            server_default="0",
        ),
        sa.Column(
            "inventory_version",
            sa.BigInteger(),
            nullable=False,
            server_default="0",
        ),
        sa.CheckConstraint("price >= 0", name="ck_products_price_nonnegative"),
        sa.CheckConstraint(
            "remaining_quantity >= 0",
            name="ck_products_remaining_quantity_nonnegative",
        ),
        sa.CheckConstraint(
            "inventory_version >= 0",
            name="ck_products_inventory_version_nonnegative",
        ),
    )
    op.create_index("ix_products_drop_id", "products", ["drop_id"])
    op.create_table(
        "processed_events",
        sa.Column("event_id", sa.String(length=128), primary_key=True),
        sa.Column("event_type", sa.String(length=128), nullable=False),
        sa.Column(
            "processed_at",
            sa.DateTime(timezone=True),
            nullable=False,
            server_default=sa.func.now(),
        ),
    )
    _seed_catalog(drops, products)


def _seed_catalog(drops: sa.Table, products: sa.Table) -> None:
    opens_at = datetime(2026, 7, 3, 10, 0, tzinfo=UTC)
    closes_at = datetime(2026, 7, 10, 10, 0, tzinfo=UTC)
    op.bulk_insert(
        drops,
        [
            {
                "id": "drop-001",
                "title": "DropMong July Limited Drop",
                "status": "OPEN",
                "opens_at": opens_at,
                "closes_at": closes_at,
                "description": (
                    "한정 수량으로 판매되는 DropMong 첫 번째 공개 드롭입니다."
                ),
            },
            {
                "id": "drop-sold-out-001",
                "title": "DropMong Sold Out Scenario Drop",
                "status": "OPEN",
                "opens_at": opens_at,
                "closes_at": closes_at,
                "description": "품절과 동시성 시나리오 검증을 위한 독립 드롭입니다.",
            },
        ],
    )
    op.bulk_insert(
        products,
        [
            {
                "id": "product-001",
                "drop_id": "drop-001",
                "name": "DropMong Starter Kit",
                "price": 50000,
                "remaining_quantity": 42,
                "inventory_version": 0,
            },
            {
                "id": "product-sold-out-001",
                "drop_id": "drop-sold-out-001",
                "name": "DropMong Concurrency Kit",
                "price": 50000,
                "remaining_quantity": 42,
                "inventory_version": 0,
            },
        ],
    )


def downgrade() -> None:
    """Reject destructive catalog migration rollback."""
    raise UnsupportedDowngradeError
