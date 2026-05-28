import json
from aiokafka import AIOKafkaProducer
from app.config import settings


async def publish_event(topic: str, payload: dict) -> bool:
    if not settings.kafka_bootstrap_servers:
        return False

    producer = AIOKafkaProducer(
        bootstrap_servers=settings.kafka_bootstrap_servers,
        value_serializer=lambda value: json.dumps(value).encode("utf-8"),
    )
    await producer.start()
    try:
        await producer.send_and_wait(topic, payload)
        return True
    finally:
        await producer.stop()
