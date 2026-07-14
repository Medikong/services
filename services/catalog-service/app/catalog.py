"""Catalog domain values."""

from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum


class DropStatus(StrEnum):
    """Lifecycle state exposed by the catalog API."""

    UPCOMING = "UPCOMING"
    OPEN = "OPEN"
    SOLD_OUT = "SOLD_OUT"
    CLOSED = "CLOSED"


class CatalogReadiness(StrEnum):
    """Catalog persistence readiness outcome."""

    READY = "READY"
    MIGRATION_REQUIRED = "MIGRATION_REQUIRED"
    DATABASE_UNAVAILABLE = "DATABASE_UNAVAILABLE"


@dataclass(frozen=True, slots=True)
class Product:
    """Product metadata with an order-owned inventory projection."""

    id: str
    name: str
    price: int
    remaining_quantity: int
    inventory_version: int


@dataclass(frozen=True, slots=True)
class DropDetail:
    """Drop metadata and its projected products."""

    id: str
    title: str
    status: DropStatus
    opens_at: datetime
    closes_at: datetime | None
    description: str
    products: tuple[Product, ...]
