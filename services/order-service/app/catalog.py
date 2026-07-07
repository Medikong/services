from dataclasses import dataclass
from typing import Final

from app.models import DropId, ProductId


@dataclass(frozen=True, slots=True)
class ProductForSale:
    drop_id: DropId
    product_id: ProductId
    unit_price: int
    remaining_quantity: int


PRODUCT_CATALOG: Final = (
    ProductForSale(
        drop_id=DropId("drop-001"),
        product_id=ProductId("product-001"),
        unit_price=50000,
        remaining_quantity=42,
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
