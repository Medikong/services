import asyncio
from collections.abc import Callable
import json
from typing import Any
from aiokafka import AIOKafkaConsumer
from kafka_utils import kafka_message_attributes, start_consumer_span
from observability import record_exception, set_current_span_attributes
from pydantic import ValidationError

from app.config import settings
from app.database import get_db
from app.services.notification_service import handle_business_event


ConsumerFactory = Callable[..., Any]


async def consume_events(
    stop_event: asyncio.Event,
    *,
    consumer_factory: ConsumerFactory = AIOKafkaConsumer,
) -> None:
    if not settings.kafka_bootstrap_servers:
        return

    consumer = consumer_factory(
        settings.reservation_created_topic,
        settings.reservation_expired_topic,
        settings.payment_approved_topic,
        settings.payment_failed_topic,
        settings.ticket_issued_topic,
        bootstrap_servers=settings.kafka_bootstrap_servers,
        group_id=settings.kafka_group_id,
        enable_auto_commit=False,
        value_deserializer=lambda value: json.loads(value.decode("utf-8")),
    )

    await consumer.start()
    try:
        async for message in consumer:
            with start_consumer_span(message):
                set_current_span_attributes({"event.type": str(message.value.get("eventType", "")) or None})
                try:
                    db = get_db()
                    await handle_business_event(db, message.value)
                except (ValidationError, ValueError) as exc:
                    record_exception(exc, service_name=settings.service_name, attributes=kafka_message_attributes(message))
                    await consumer.commit()
                    continue
                except Exception as exc:
                    record_exception(exc, service_name=settings.service_name, attributes=kafka_message_attributes(message))
                    raise
                await consumer.commit()
            if stop_event.is_set():
                break
    finally:
        await consumer.stop()
