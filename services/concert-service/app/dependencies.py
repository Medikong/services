from typing import Annotated

from fastapi import Header

from app.database import get_db


def get_provider_id(x_provider_id: Annotated[str | None, Header(alias="X-Provider-Id")] = None) -> str:
    return x_provider_id or "provider-001"


__all__ = ["get_db", "get_provider_id"]
