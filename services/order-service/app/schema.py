from __future__ import annotations

from typing import Final

import anyio
import sqlalchemy as sa
from sqlalchemy.exc import DBAPIError, TimeoutError as SQLAlchemyTimeoutError
from sqlalchemy.ext.asyncio import AsyncEngine

ORDER_SCHEMA_REVISION: Final = "20260715_0003"
DATABASE_READINESS_TIMEOUT_SECONDS: Final = 2.0


async def database_migration_is_current(engine: AsyncEngine) -> bool:
    version_column = sa.column("version_num", sa.String())
    version_table = sa.table("alembic_version", version_column)
    try:
        with anyio.fail_after(DATABASE_READINESS_TIMEOUT_SECONDS):
            async with engine.connect() as connection:
                revision = await connection.scalar(
                    sa.select(version_column).select_from(version_table),
                )
    except (DBAPIError, SQLAlchemyTimeoutError, ConnectionError, TimeoutError):
        return False
    return revision == ORDER_SCHEMA_REVISION
