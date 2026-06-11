from motor.motor_asyncio import AsyncIOMotorClient, AsyncIOMotorDatabase
from observability import instrument_motor_client

from app.config import settings

client: AsyncIOMotorClient | None = None


def get_db() -> AsyncIOMotorDatabase:
    if client is None:
        raise RuntimeError("MongoDB client is not connected")
    return client[settings.mongodb_db_name]


async def connect_db() -> None:
    global client
    client = AsyncIOMotorClient(settings.mongodb_url)
    instrument_motor_client(client)


def close_db() -> None:
    global client
    if client is not None:
        client.close()
        client = None
