import asyncio

from app.worker import run_worker


def main() -> None:
    asyncio.run(run_worker())


if __name__ == "__main__":
    main()
