from dataclasses import dataclass
from typing import Final

from app.models import DropId, ProductId


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
