from typing import final, override


@final
class UnsupportedDowngradeError(Exception):
    __slots__ = ("revision_id",)

    def __init__(self, revision_id: str) -> None:
        self.revision_id = revision_id
        super().__init__(str(self))

    @override
    def __str__(self) -> str:
        return f"Downgrade is unsupported for order schema revision {self.revision_id}"


@final
class LegacySchemaError(Exception):
    __slots__ = ("detail",)

    def __init__(self, detail: str) -> None:
        self.detail = detail
        super().__init__(str(self))

    @override
    def __str__(self) -> str:
        return f"legacy order schema is incompatible: {self.detail}"


@final
class LegacyInventoryContradictionError(Exception):
    __slots__ = ("drop_id", "product_id", "reserved_quantity", "sold_quantity")

    def __init__(
        self,
        drop_id: str,
        product_id: str,
        reserved_quantity: int,
        sold_quantity: int,
    ) -> None:
        self.drop_id = drop_id
        self.product_id = product_id
        self.reserved_quantity = reserved_quantity
        self.sold_quantity = sold_quantity
        super().__init__(str(self))

    @override
    def __str__(self) -> str:
        return (
            "legacy inventory contradiction for "
            f"{self.drop_id}/{self.product_id}: reserved={self.reserved_quantity}, "
            f"sold={self.sold_quantity}, total=42; repair rows before retrying"
        )
