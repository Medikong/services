"""PostgreSQL catalog persistence adapter."""

from datetime import datetime
from typing import Final, final

import anyio
from contracts import InventoryChangedEvent
from sqlalchemy import (
    BigInteger,
    CheckConstraint,
    DateTime,
    ForeignKey,
    Integer,
    String,
    Text,
    column,
    func,
    literal,
    select,
    table,
)
from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.exc import DBAPIError, ProgrammingError
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker
from sqlalchemy.orm import (
    DeclarativeBase,
    Mapped,
    mapped_column,
    relationship,
    selectinload,
)

from app.catalog import CatalogReadiness, DropDetail, DropStatus, Product

MIGRATION_HEAD: Final = "0002_inventory_projection"
READINESS_TIMEOUT_SECONDS: Final = 1.0


class Base(DeclarativeBase):
    """Catalog SQLAlchemy metadata root."""


@final
class DropRow(Base):
    """Persisted drop metadata."""

    __tablename__ = "drops"

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    title: Mapped[str] = mapped_column(String(255))
    status: Mapped[str] = mapped_column(String(16))
    opens_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    closes_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    description: Mapped[str] = mapped_column(Text)
    products: Mapped[list["ProductRow"]] = relationship(
        back_populates="drop",
        order_by="ProductRow.id",
        lazy="selectin",
    )


@final
class ProductRow(Base):
    """Persisted Catalog-owned product metadata."""

    __tablename__ = "products"
    __table_args__: tuple[CheckConstraint, ...] = (
        CheckConstraint("price >= 0", name="ck_products_price_nonnegative"),
    )

    id: Mapped[str] = mapped_column(String(64), primary_key=True)
    drop_id: Mapped[str] = mapped_column(
        ForeignKey("drops.id", ondelete="CASCADE"),
        index=True,
    )
    name: Mapped[str] = mapped_column(String(255))
    price: Mapped[int] = mapped_column(Integer)
    drop: Mapped[DropRow] = relationship(back_populates="products")
    inventory: Mapped["InventoryProjectionRow | None"] = relationship(
        back_populates="product",
        lazy="joined",
        uselist=False,
    )


@final
class InventoryProjectionRow(Base):
    """Order-owned inventory values projected into Catalog storage."""

    __tablename__ = "inventory_projections"
    __table_args__: tuple[CheckConstraint, ...] = (
        CheckConstraint(
            "remaining_quantity >= 0",
            name="ck_inventory_projections_remaining_nonnegative",
        ),
        CheckConstraint(
            "inventory_version >= 0",
            name="ck_inventory_projections_version_nonnegative",
        ),
    )

    product_id: Mapped[str] = mapped_column(
        ForeignKey("products.id", ondelete="CASCADE"),
        primary_key=True,
    )
    drop_id: Mapped[str] = mapped_column(
        ForeignKey("drops.id", ondelete="CASCADE"),
        index=True,
    )
    remaining_quantity: Mapped[int] = mapped_column(Integer)
    inventory_version: Mapped[int] = mapped_column(BigInteger)
    product: Mapped[ProductRow] = relationship(back_populates="inventory")


@final
class ProcessedEventRow(Base):
    """Idempotency record for future inventory projection consumers."""

    __tablename__ = "processed_events"

    event_id: Mapped[str] = mapped_column(String(128), primary_key=True)
    event_type: Mapped[str] = mapped_column(String(128))
    processed_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True),
        server_default=func.now(),
    )


@final
class PostgresCatalogRepository:
    """PostgreSQL-backed catalog query adapter."""

    def __init__(self, sessions: async_sessionmaker[AsyncSession]) -> None:
        """Store the async session factory."""
        self._sessions = sessions

    async def list_drops(self) -> tuple[DropDetail, ...]:
        """Return all drops and products in stable identifier order."""
        async with self._sessions() as session:
            rows = await session.scalars(
                select(DropRow)
                .options(selectinload(DropRow.products))
                .order_by(DropRow.id)
            )
            return tuple(_to_drop(row) for row in rows.unique().all())

    async def get_drop(self, drop_id: str) -> DropDetail | None:
        """Return one drop when its identifier exists."""
        async with self._sessions() as session:
            row = await session.scalar(
                select(DropRow)
                .options(selectinload(DropRow.products))
                .where(DropRow.id == drop_id)
            )
            return None if row is None else _to_drop(row)

    async def apply_inventory_changed(self, event: InventoryChangedEvent) -> None:
        """Record and conditionally apply one absolute inventory projection."""
        async with self._sessions.begin() as session:
            processed = await session.get(ProcessedEventRow, event.eventId)
            if processed is not None:
                return
            session.add(
                ProcessedEventRow(
                    event_id=event.eventId,
                    event_type=event.eventType,
                ),
            )
            product_exists = await session.scalar(
                select(ProductRow.id).where(
                    ProductRow.id == event.productId,
                    ProductRow.drop_id == event.dropId,
                ),
            )
            if product_exists is None:
                return
            statement = insert(InventoryProjectionRow).values(
                product_id=event.productId,
                drop_id=event.dropId,
                remaining_quantity=event.remainingQuantity,
                inventory_version=event.inventoryVersion,
            )
            _ = await session.execute(
                statement.on_conflict_do_update(
                    index_elements=[InventoryProjectionRow.product_id],
                    set_={
                        "drop_id": statement.excluded.drop_id,
                        "remaining_quantity": statement.excluded.remaining_quantity,
                        "inventory_version": statement.excluded.inventory_version,
                    },
                    where=(
                        statement.excluded.inventory_version
                        > InventoryProjectionRow.inventory_version
                    ),
                ),
            )

    async def readiness(self) -> CatalogReadiness:
        """Return bounded database and migration readiness."""
        try:
            with anyio.fail_after(READINESS_TIMEOUT_SECONDS):
                async with self._sessions() as session:
                    version_table = table(
                        "alembic_version",
                        column("version_num", String()),
                    )
                    ready = await session.scalar(
                        select(literal(value=True))
                        .select_from(version_table)
                        .where(version_table.c.version_num == MIGRATION_HEAD),
                    )
                    return (
                        CatalogReadiness.READY
                        if ready is True
                        else CatalogReadiness.MIGRATION_REQUIRED
                    )
        except ProgrammingError:
            return CatalogReadiness.MIGRATION_REQUIRED
        except (ConnectionRefusedError, TimeoutError, DBAPIError):
            return CatalogReadiness.DATABASE_UNAVAILABLE


def _to_drop(row: DropRow) -> DropDetail:
    products = tuple(
        Product(
            id=product.id,
            name=product.name,
            price=product.price,
            remaining_quantity=(
                product.inventory.remaining_quantity
                if product.inventory is not None
                else 0
            ),
            inventory_version=(
                product.inventory.inventory_version
                if product.inventory is not None
                else 0
            ),
        )
        for product in row.products
    )
    return DropDetail(
        id=row.id,
        title=row.title,
        status=(
            DropStatus.SOLD_OUT
            if products and all(product.remaining_quantity == 0 for product in products)
            else DropStatus(row.status)
        ),
        opens_at=row.opens_at,
        closes_at=row.closes_at,
        description=row.description,
        products=products,
    )
