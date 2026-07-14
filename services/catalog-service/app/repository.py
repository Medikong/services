"""Catalog persistence contract."""

from typing import Protocol

from app.catalog import CatalogReadiness, DropDetail


class CatalogRepository(Protocol):
    """Persistence contract used by the catalog store."""

    async def list_drops(self) -> tuple[DropDetail, ...]:
        """Return catalog drops in stable order."""
        ...

    async def get_drop(self, drop_id: str) -> DropDetail | None:
        """Return one drop when present."""
        ...

    async def readiness(self) -> CatalogReadiness:
        """Return the persistence readiness outcome."""
        ...
