"""Upgrade-only catalog migration CLI."""

import os
import sys
from collections.abc import Sequence
from pathlib import Path

from alembic import command
from alembic.config import Config

from app.db import DEFAULT_DATABASE_URL


def upgrade(database_url: str | None = None) -> None:
    """Upgrade the configured catalog database to the latest revision."""
    service_root = Path(__file__).resolve().parents[1]
    config = Config(service_root / "alembic.ini")
    url = database_url or os.getenv("DATABASE_URL", DEFAULT_DATABASE_URL)
    config.set_main_option("sqlalchemy.url", url.replace("%", "%%"))
    command.upgrade(config, "head")


def main(arguments: Sequence[str] | None = None) -> int:
    """Run the explicit, upgrade-only migration command."""
    values = tuple(sys.argv[1:] if arguments is None else arguments)
    if values != ("upgrade",):
        _ = sys.stderr.write("usage: python -m app.migrations upgrade\n")
        _ = sys.stderr.write("catalog migrations do not support downgrade\n")
        return 2
    upgrade()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
