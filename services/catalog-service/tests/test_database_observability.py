import pytest

from app.db import create_database


@pytest.mark.anyio
async def test_multiple_engines_each_receive_sqlalchemy_trace_listener() -> None:
    first = create_database("postgresql+asyncpg://app:app@localhost/catalog_first")
    second = create_database("postgresql+asyncpg://app:app@localhost/catalog_second")

    try:
        assert len(first.engine.sync_engine.dispatch.before_cursor_execute) == 1
        assert len(second.engine.sync_engine.dispatch.before_cursor_execute) == 1
    finally:
        await first.engine.dispose()
        await second.engine.dispose()
