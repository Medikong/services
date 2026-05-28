from typing import Annotated

from fastapi import Header

from app.database import get_db


def get_user_id(x_user_id: Annotated[str | None, Header(alias="X-User-Id")] = None) -> str:
    return x_user_id or "user-001"


__all__ = ["get_db", "get_user_id"]
