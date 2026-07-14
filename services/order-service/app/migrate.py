from __future__ import annotations

import logging
import sys
from pathlib import Path
from typing import Final, final, override

from alembic import command
from alembic.config import Config

from migrations.errors import UnsupportedDowngradeError

SERVICE_ROOT: Final = Path(__file__).parents[1]
LOGGER: Final = logging.getLogger("alembic")


@final
class MigrationUsageError(Exception):
    __slots__ = ("arguments",)

    def __init__(self, arguments: tuple[str, ...]) -> None:
        self.arguments = arguments
        super().__init__(str(self))

    @override
    def __str__(self) -> str:
        return "usage: python -m app.migrate upgrade [head] | downgrade <revision>"


def main(arguments: tuple[str, ...] | None = None) -> int:
    parsed_arguments = tuple(sys.argv[1:]) if arguments is None else arguments
    configuration = Config(SERVICE_ROOT / "alembic.ini")
    try:
        match parsed_arguments:
            case ("upgrade",):
                command.upgrade(configuration, "head")
            case ("upgrade", revision_id):
                command.upgrade(configuration, revision_id)
            case ("downgrade", revision_id):
                command.downgrade(configuration, revision_id)
            case _:
                raise MigrationUsageError(arguments=parsed_arguments)
    except MigrationUsageError as error:
        print(error, file=sys.stderr)
        return 2
    except UnsupportedDowngradeError as error:
        LOGGER.error("%s", error)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
