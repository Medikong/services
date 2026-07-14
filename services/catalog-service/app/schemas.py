"""Catalog HTTP response schemas and domain conversion."""

from datetime import datetime
from typing import ClassVar

from pydantic import BaseModel, ConfigDict, Field

from app.catalog import DropDetail, DropStatus, Product


class ProductSummary(BaseModel):
    """Product metadata and inventory projection response."""

    model_config: ClassVar[ConfigDict] = ConfigDict(
        frozen=True,
        populate_by_name=True,
    )

    id: str
    name: str
    price: int = Field(ge=0)
    remaining_quantity: int = Field(ge=0, serialization_alias="remainingQuantity")
    inventory_version: int = Field(ge=0, serialization_alias="inventoryVersion")


class DropSummary(BaseModel):
    """Drop list item response."""

    model_config: ClassVar[ConfigDict] = ConfigDict(
        frozen=True,
        populate_by_name=True,
    )

    id: str
    title: str
    status: DropStatus
    opens_at: datetime = Field(serialization_alias="opensAt")
    closes_at: datetime | None = Field(default=None, serialization_alias="closesAt")
    products: tuple[ProductSummary, ...]


class DropResponse(DropSummary):
    """Detailed drop response."""

    description: str


class PageInfo(BaseModel):
    """Cursor page metadata."""

    model_config: ClassVar[ConfigDict] = ConfigDict(
        frozen=True,
        populate_by_name=True,
    )

    next_cursor: str | None = Field(default=None, serialization_alias="nextCursor")
    has_next: bool = Field(serialization_alias="hasNext")


class DropListResponse(BaseModel):
    """Paginated drop list response."""

    model_config: ClassVar[ConfigDict] = ConfigDict(
        frozen=True,
        populate_by_name=True,
    )

    data: tuple[DropSummary, ...]
    page_info: PageInfo = Field(serialization_alias="pageInfo")


class DropDetailResponse(BaseModel):
    """Drop detail envelope."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    data: DropResponse


class HealthResponse(BaseModel):
    """Liveness response."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    status: str
    service: str
    timestamp: datetime


class ReadinessResponse(BaseModel):
    """Migration-aware readiness response."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    status: str
    service: str
    checks: dict[str, str]
    timestamp: datetime


def product_response(product: Product) -> ProductSummary:
    """Convert a domain product to its HTTP response."""
    return ProductSummary(
        id=product.id,
        name=product.name,
        price=product.price,
        remaining_quantity=product.remaining_quantity,
        inventory_version=product.inventory_version,
    )


def drop_summary(drop: DropDetail) -> DropSummary:
    """Convert a domain drop to its list representation."""
    return DropSummary(
        id=drop.id,
        title=drop.title,
        status=drop.status,
        opens_at=drop.opens_at,
        closes_at=drop.closes_at,
        products=tuple(product_response(product) for product in drop.products),
    )


def drop_response(drop: DropDetail) -> DropResponse:
    """Convert a domain drop to its detail representation."""
    return DropResponse(
        id=drop.id,
        title=drop.title,
        status=drop.status,
        opens_at=drop.opens_at,
        closes_at=drop.closes_at,
        products=tuple(product_response(product) for product in drop.products),
        description=drop.description,
    )
