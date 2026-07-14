"""Catalog application query boundary."""

from typing import final

from app.catalog import CatalogReadiness, DropDetail
from app.repository import CatalogRepository


@final
class CatalogStore:
    """Catalog query boundary independent of the persistence adapter."""

    def __init__(self, repository: CatalogRepository) -> None:
        """Store the catalog persistence contract."""
        self._repository = repository

    async def list_drops(self) -> tuple[DropDetail, ...]:
        """Return all persisted drops."""
        return await self._repository.list_drops()

    async def get_drop(self, drop_id: str) -> DropDetail | None:
        """Return one persisted drop when present."""
        return await self._repository.get_drop(drop_id)

    async def readiness(self) -> CatalogReadiness:
        """Return persistence readiness."""
        return await self._repository.readiness()
