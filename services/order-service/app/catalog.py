import os
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Final

from pydantic import BaseModel, ConfigDict, Field, TypeAdapter, ValidationError
from pydantic.aliases import AliasChoices

from app.models import DropId, ProductId


ORDER_EXTRA_PRODUCTS_JSON_ENV: Final = "ORDER_EXTRA_PRODUCTS_JSON"


@dataclass(frozen=True, slots=True)
class ProductCatalogConfigurationError(RuntimeError):
    value: str
    detail: str

    def __str__(self) -> str:
        return (
            f"{ORDER_EXTRA_PRODUCTS_JSON_ENV} must be a JSON array of products; "
            f"got {self.value!r}: {self.detail}"
        )


class _ConfiguredProduct(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)

    drop_id: str = Field(
        min_length=1,
        max_length=64,
        validation_alias=AliasChoices("drop_id", "dropId"),
    )
    product_id: str = Field(
        min_length=1,
        max_length=64,
        validation_alias=AliasChoices("product_id", "productId"),
    )
    unit_price: int = Field(
        gt=0,
        validation_alias=AliasChoices("unit_price", "unitPrice"),
    )


_CONFIGURED_PRODUCTS_ADAPTER: TypeAdapter[tuple[_ConfiguredProduct, ...]] = (
    TypeAdapter(tuple[_ConfiguredProduct, ...])
)


@dataclass(frozen=True, slots=True)
class ProductForSale:
    drop_id: DropId
    product_id: ProductId
    unit_price: int


PRODUCTS_FOR_SALE: Final = (
    ProductForSale(
        drop_id=DropId("drop-001"),
        product_id=ProductId("product-001"),
        unit_price=50000,
    ),
    ProductForSale(
        drop_id=DropId("drop-sold-out-001"),
        product_id=ProductId("product-sold-out-001"),
        unit_price=50000,
    ),
)


def product_for(
    catalog: tuple[ProductForSale, ...],
    drop_id: DropId,
    product_id: ProductId,
) -> ProductForSale | None:
    for product in catalog:
        if product.drop_id == drop_id and product.product_id == product_id:
            return product
    return None


def catalog_from_env(
    env: Mapping[str, str] | None = None,
) -> tuple[ProductForSale, ...]:
    source = os.environ if env is None else env
    raw = source.get(ORDER_EXTRA_PRODUCTS_JSON_ENV, "").strip()
    if raw == "":
        return PRODUCTS_FOR_SALE

    try:
        configured_products = _CONFIGURED_PRODUCTS_ADAPTER.validate_json(raw)
    except ValidationError as error:
        raise ProductCatalogConfigurationError(raw, str(error)) from error

    extras = tuple(
        ProductForSale(
            drop_id=DropId(item.drop_id),
            product_id=ProductId(item.product_id),
            unit_price=item.unit_price,
        )
        for item in configured_products
    )
    _reject_duplicate_products(raw, extras)
    return PRODUCTS_FOR_SALE + extras


def _reject_duplicate_products(
    raw: str,
    extras: tuple[ProductForSale, ...],
) -> None:
    known_keys = {
        (product.drop_id, product.product_id) for product in PRODUCTS_FOR_SALE
    }
    for product in extras:
        key = (product.drop_id, product.product_id)
        if key in known_keys:
            raise ProductCatalogConfigurationError(
                raw,
                f"duplicate product ({product.drop_id!r}, {product.product_id!r})",
            )
        known_keys.add(key)
