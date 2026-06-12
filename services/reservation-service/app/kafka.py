from fastapi import Request
from kafka_utils import TraceAwareKafkaProducer, create_kafka_producer

from app.config import settings


KafkaProducer = TraceAwareKafkaProducer | None


def create_producer() -> KafkaProducer:
    return create_kafka_producer(
        settings.kafka_bootstrap_servers,
        client_id=settings.service_name,
    )


def get_kafka_producer(request: Request) -> KafkaProducer:
    return request.app.state.kafka_producer
