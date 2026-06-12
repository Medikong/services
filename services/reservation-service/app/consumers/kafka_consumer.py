import asyncio
import json
import logging
from aiokafka import AIOKafkaConsumer
from kafka_utils import kafka_message_attributes, start_consumer_span
from observability import record_exception, set_current_span_attributes

logger = logging.getLogger(__name__)

async def consume_ticket_issued(
    stop_event: asyncio.Event,
    *,
    bootstrap_servers: str,
    group_id: str,
    topic: str,
    session_factory,
    service_name: str = "reservation-service",
) -> None:
    if not bootstrap_servers:
        return

    consumer = AIOKafkaConsumer(
        topic,
        bootstrap_servers=bootstrap_servers,
        group_id=group_id,
        value_deserializer=lambda v: json.loads(v.decode("utf-8")),
    )
    await consumer.start()
    try:
        async for message in consumer:
            try:
                with start_consumer_span(message):
                    payload = message.value
                    set_current_span_attributes({"event.type": str(payload.get("eventType", "")) or None})
                    reservation_id = payload.get("reservationId")
                    if reservation_id:
                        with session_factory() as db:
                            from app.services.reservations import ReservationCommandService
                            svc = ReservationCommandService(db)
                            svc.confirm_reservation(reservation_id)
                            logger.info("reservation confirmed: %s", reservation_id)
            except Exception as exc:
                record_exception(
                    exc,
                    service_name=service_name,
                    attributes=kafka_message_attributes(message),
                )
                logger.exception("ticket_issued_event_handling_failed")
            if stop_event.is_set():
                break
    finally:
        await consumer.stop()
